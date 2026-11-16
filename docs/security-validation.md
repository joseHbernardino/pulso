# Validação de segurança — 16/11/2026

A revisão cobre as dependências Go, as actions utilizadas pelo projeto e os
checks locais de compilação, análise estática e testes. Os alertas privados do
Dependabot não puderam ser consultados pela API do GitHub neste ambiente; os
resultados abaixo vêm do código local e da base oficial de vulnerabilidades Go.

## Correções

- Atualizado `github.com/go-chi/chi/v5` de 5.2.5 para 5.5.0, corrigindo os avisos
  [GO-2026-5774](https://pkg.go.dev/vuln/GO-2026-5774),
  [GO-2026-5775](https://pkg.go.dev/vuln/GO-2026-5775) e
  [GO-2026-5777](https://pkg.go.dev/vuln/GO-2026-5777).
- Elevada a versão mínima para Go 1.26.11. CI e publicação agora leem `go.mod`,
  evitando a divergência anterior entre dependências Go 1.26 e workflows 1.25.
- Atualizadas as actions abaixo. Cada referência permanece fixada no SHA completo
  do commit, conferido com a tag do repositório oficial.
- Adicionado `govulncheck` ao CI, inclusive na execução semanal e manual.
- Limitadas as permissões padrão a leitura; somente o job de publicação recebe
  escrita em `contents`.
- Incluídos frontend, configuração do GitHub e arquivos Nix nos filtros do CI.
- Atualizados o lock e o hash das dependências Nix. O macOS Intel usa a linha
  Nixpkgs 26.05 ainda mantida, preservando a plataforma após sua remoção na 26.11.
- Corrigidos erros apontados pelo linter no fechamento de recursos e nos testes.
  `make lint` agora retorna falha quando o linter falha ou está ausente.

| Action | Versão |
| --- | --- |
| actions/checkout | 7.0.1 |
| actions/setup-go | 7.0.0 |
| golangci/golangci-lint-action | 9.3.0 |
| DeterminateSystems/nix-installer-action | 23 |
| DeterminateSystems/magic-nix-cache-action | 15 |
| actions/upload-artifact | 7.0.2 |
| actions/download-artifact | 8.0.2 |
| softprops/action-gh-release | 3.0.4 |

## Verificações executadas

| Verificação | Resultado |
| --- | --- |
| `go mod verify` | Todos os módulos verificados |
| `go test -race -count=1 -timeout 120s ./...` | Aprovado em todos os pacotes com testes, incluindo E2E |
| `go vet ./...` | Sem erros |
| golangci-lint 2.15.0 | Zero ocorrências |
| actionlint 1.7.12 | Workflows válidos |
| `gofmt` e `git diff --check` | Sem pendências |
| `govulncheck -test ./...` | Zero vulnerabilidades em símbolos utilizados e pacotes importados; um aviso em módulo não importado, descrito abaixo |
| `govulncheck -mode=binary` no executável com símbolos | Zero vulnerabilidades |
| Build sem CGO: Linux amd64/arm64, macOS amd64/arm64, Windows amd64 | Aprovado nas cinco combinações |
| Scripts reais de empacotamento e checksums da publicação | Cinco arquivos compactados e cinco executáveis gerados; dez checksums verificados |
| `nix flake check --all-systems` | Pacotes e ambientes das quatro plataformas avaliados |
| `nix build` e execução de `pulso version` | Pacote Linux compilado e executado |
| Painel local atualizado | Contas existentes preservadas; APIs e WebSocket rejeitam acesso sem autenticação com HTTP 401 |

Os binários de macOS e Windows foram compilados neste Linux; a execução nativa
nesses sistemas e a publicação real pelo GitHub não foram realizadas. A
verificação de workflows e o empacotamento local não substituem a execução no
GitHub depois do push. Estas verificações não constituem garantia de ausência de
falhas desconhecidas.

## Aviso da biblioteca MCP sem correção publicada

[GO-2026-6911](https://pkg.go.dev/vuln/GO-2026-6911) afeta o **cliente**
`github.com/mark3labs/mcp-go/client/transport`: um redirecionamento HTTP pode
encaminhar credenciais a outro destino. A versão 1.5.0 é a versão disponível
avaliada e a base oficial ainda não informa uma versão corrigida.

O Pulso implementa um **servidor** MCP. A análise de dependências de
`./cmd/pulso` confirma que o pacote cliente afetado não é importado. A análise de
código e a análise do binário com símbolos não encontram as funções vulneráveis.
O aviso continua listado no nível do módulo e deve permanecer acompanhado, sem
ser apresentado como uma correção do código da biblioteca. Não se deve introduzir
esse cliente sem revisar o aviso e suas proteções contra redirecionamento.

O scanner de binários sem tabela de símbolos (`-ldflags '-s -w'`, como nos
artefatos de release) faz uma análise conservadora e pode reportar esse módulo
mesmo sem o pacote vulnerável compilado. Por isso a validação binária também foi
feita em uma compilação equivalente com os símbolos preservados. O aviso não foi
suprimido na configuração do CI.
