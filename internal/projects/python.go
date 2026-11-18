package projects

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Keep the build tool separate from application dependencies: a project's
// locked packages must not upgrade or uninstall Poetry's own dependencies.
const poetryVersion = "2.5.1"

type pythonDependencies struct {
	manager string
	files   []string
	setup   string
	install string
}

func pythonDependencyPlan(root string) (pythonDependencies, error) {
	files := map[string]bool{}
	for _, name := range []string{"requirements.txt", "pyproject.toml", "poetry.lock"} {
		info, err := os.Lstat(filepath.Join(root, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return pythonDependencies{}, fmt.Errorf("ler %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return pythonDependencies{}, fmt.Errorf("%s deve ser um arquivo regular dentro da pasta do projeto", name)
		}
		files[name] = true
	}
	var manifest struct {
		BuildSystem struct {
			Backend string `toml:"build-backend"`
		} `toml:"build-system"`
		Tool struct {
			Poetry map[string]any `toml:"poetry"`
		} `toml:"tool"`
	}
	if files["pyproject.toml"] {
		data, err := readManifest(filepath.Join(root, "pyproject.toml"))
		if err != nil {
			return pythonDependencies{}, fmt.Errorf("pyproject.toml: %w", err)
		}
		if err = toml.Unmarshal(data, &manifest); err != nil {
			return pythonDependencies{}, fmt.Errorf("pyproject.toml inválido: %w", err)
		}
	}
	if files["poetry.lock"] || manifest.Tool.Poetry != nil || manifest.BuildSystem.Backend == "poetry.core.masonry.api" {
		if !files["pyproject.toml"] {
			return pythonDependencies{}, fmt.Errorf("poetry.lock encontrado sem pyproject.toml; selecione a pasta que contém os dois arquivos")
		}
		required := []string{"pyproject.toml"}
		message := "Poetry detectado; sem poetry.lock, as versões serão resolvidas durante a compilação"
		if files["poetry.lock"] {
			required = append(required, "poetry.lock")
			message = "Poetry detectado; instalando as versões do poetry.lock"
		}
		return pythonDependencies{
			manager: "Poetry", files: required,
			setup: "RUN python -m venv /opt/pulso-poetry && /opt/pulso-poetry/bin/pip install --no-cache-dir poetry==" + poetryVersion + "\n" +
				"RUN python -m venv /opt/venv\nENV VIRTUAL_ENV=/opt/venv\nENV PATH=\"/opt/venv/bin:/opt/pulso-poetry/bin:$PATH\"\n",
			install: "RUN echo '" + message + "' && /opt/pulso-poetry/bin/poetry install --only main --no-root --no-interaction --no-ansi\n",
		}, nil
	}
	if files["requirements.txt"] {
		return pythonDependencies{manager: "pip / requirements.txt", files: []string{"requirements.txt"},
			install: "RUN echo 'Dependências Python: requirements.txt' && python -m pip install --no-cache-dir -r requirements.txt\n"}, nil
	}
	if files["pyproject.toml"] {
		return pythonDependencies{manager: "pip / pyproject.toml", files: []string{"pyproject.toml"},
			install: "RUN echo 'Dependências Python: pyproject.toml' && python -m pip install --no-cache-dir .\n"}, nil
	}
	// A common mistake is selecting scripts/ or src/ instead of the project
	// root. Diagnose it without uploading or executing anything from the parent.
	parent := filepath.Dir(root)
	for depth := 0; depth < 3 && parent != root; depth++ {
		for _, name := range []string{"requirements.txt", "pyproject.toml", "poetry.lock"} {
			if info, err := os.Lstat(filepath.Join(parent, name)); err == nil && info.Mode().IsRegular() {
				return pythonDependencies{}, fmt.Errorf("a pasta selecionada não contém um manifesto Python, mas há %s em uma pasta acima; selecione a raiz do projeto para incluir o código e as dependências", name)
			}
		}
		root, parent = parent, filepath.Dir(parent)
	}
	return pythonDependencies{manager: "biblioteca padrão", install: "RUN echo 'Nenhum requirements.txt ou pyproject.toml na raiz; nenhuma dependência externa será instalada'\n"}, nil
}
