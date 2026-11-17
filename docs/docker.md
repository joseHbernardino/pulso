# Pulso em Docker

O Pulso pode rodar como um serviço Docker. O host precisa do Docker Engine e do
plugin Docker Compose; Go, Python e Node.js não precisam estar instalados no host.
A imagem inclui o binário compilado, Node.js 24, npm, Python 3 e suporte a venv.

O Pulso gerencia programas **dentro do seu contêiner**. Ele não controla os
processos do host nem cria um contêiner independente para cada aplicação. Os
programas compartilham o ambiente da imagem. Outros interpretadores e as
bibliotecas de cada projeto precisam ser adicionados à imagem ou à pasta do projeto.

A configuração inclui:

- Painel em `0.0.0.0:5011` no host, encaminhado para a porta interna `9615`.
- Login e permissões de administrador/visualização existentes no Pulso.
- Volume persistente para contas SQLite, sessões, cadastros, logs e recuperação.
- Pasta de projetos montada em `/apps`, incluindo os arquivos `.env` editáveis.
- Usuário sem privilégios, com UID/GID `10001:10001`.
- Reinício automático do contêiner com `unless-stopped` e encerramento por SIGTERM.

## Preparar o Linux

Instale o Engine e o plugin Compose conforme a documentação da sua distribuição:
[Docker Engine no RHEL](https://docs.docker.com/engine/install/rhel/) e
[plugin Compose no Linux](https://docs.docker.com/compose/install/linux/).

Confira os comandos e habilite o Docker para iniciar com a máquina:

```bash
docker version
docker compose version
sudo systemctl enable --now docker
```

Entre na pasta do repositório que contém `compose.yaml`:

```bash
cd /root/pulso
cp docker.env.example .env
sudo install -d -m 0750 -o 10001 -g 10001 applications
```

Edite `.env` para ajustar porta, endereço, imagem ou pasta de aplicações. Esse
arquivo configura o Compose e é diferente dos `.env` de cada aplicação.

```dotenv
PULSO_IMAGE=ghcr.io/josehbernardino/pulso:latest
PULSO_BIND=0.0.0.0
PULSO_PORT=5011
PULSO_APPS_DIR=./applications
```

A pasta de aplicações deve existir antes de subir. O Compose usa o rótulo SELinux
`Z` para essa pasta no RHEL e distribuições com SELinux. Use uma pasta dedicada a
este contêiner; o rótulo é privado a ele. Não é necessário desabilitar o SELinux.
A opção é ignorada quando o host não usa SELinux, conforme a
[referência de volumes do Compose](https://docs.docker.com/reference/compose-file/services/#volumes).

Se outro Pulso já estiver usando a porta 5011, escolha outra porta para avaliar
esta instalação. A instalação Docker começa com um volume de dados próprio;
ela não importa automaticamente as contas e os processos da instalação nativa.

## Subir a partir do código, antes de publicar a imagem

Esta opção funciona imediatamente com os arquivos do repositório:

```bash
docker compose -f compose.yaml -f compose.build.yaml up -d --build
docker compose -f compose.yaml -f compose.build.yaml ps
```

O arquivo adicional usa `pulso:local`, compilado neste checkout. O Go é usado
apenas na etapa de build e não entra na imagem final.

Depois de alterar o código, atualize essa instalação local com:

```bash
git pull --ff-only
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

## Subir a imagem publicada

O workflow [Container](../.github/workflows/container.yml) testa a imagem em PRs e
na branch principal. Ao enviar uma tag de versão como `v0.2.0`, ele publica no
GitHub Container Registry imagens para `linux/amd64` e `linux/arm64`. Versões
estáveis também atualizam `latest`; prévias como `v0.2.0-rc.1` não alteram `latest`.
A publicação só ocorre depois que os testes da imagem passam.

A primeira imagem precisa ser publicada para estes comandos funcionarem:

```bash
docker compose pull pulso
docker compose up -d
```

Se o pacote estiver privado no GitHub, autentique-se no GHCR ou altere sua
visibilidade para pública em **Packages → pulso → Package settings**. Pacotes
públicos do Container Registry permitem download sem login, conforme a
[documentação do GitHub](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

Em um fork, ajuste `PULSO_IMAGE` para `ghcr.io/seu-usuario/seu-repositorio:latest`,
sempre em letras minúsculas. O workflow usa automaticamente o nome do repositório.

## Primeiro acesso

Acesse `http://IP_DO_SERVIDOR:5011` e crie o administrador. Consulte o código de
configuração inicial com:

```bash
docker compose exec pulso cat /var/lib/pulso/security/setup-token
```

O código deixa de ser necessário depois da criação do administrador. Veja também
[login e permissões](access-control.md).

## Cadastrar aplicações e editar o .env

Coloque os projetos na pasta `applications` do host. Para um projeto `minha-api`,
os caminhos ficam assim:

| No host | No painel / contêiner |
| --- | --- |
| `/root/pulso/applications/minha-api` | `/apps/minha-api` |
| `/root/pulso/applications/minha-api/app.py` | `/apps/minha-api/app.py` |
| `/root/pulso/applications/minha-api/.env` | `/apps/minha-api/.env` |

### Reconhecer caminhos de vários projetos

O painel oferece **Selecionar** nos campos de pasta, programa, interpretador e
`.env`. O seletor lista as pastas acessíveis ao Pulso e aceita navegação por
subpastas. Ao selecionar um script com a pasta de trabalho vazia, o formulário
preenche a pasta do script. A leitura desses metadados é exclusiva de administradores.

No Linux, o Pulso também identifica volumes de projetos já montados usando as
informações de montagem do kernel. Quando a origem pode ser reconhecida, um
caminho do host é traduzido para o destino correspondente no contêiner. Isso
não exige acesso ao socket Docker. Em sistemas de arquivos onde a origem
reportada difere do caminho usado no host, configure o caminho explicitamente.

Para disponibilizar todos os serviços de uma pasta, configure uma única raiz
absoluta em `.env` do Compose:

```dotenv
PULSO_APPS_DIR=/ctmagent/develop/Servicos
```

Depois execute `docker compose up -d`. A pasta precisa existir e estar acessível
ao usuário do contêiner. Com a imagem atualizada, estes caminhos são equivalentes
no painel:

| Caminho informado do host | Caminho usado pelo Pulso |
| --- | --- |
| `/ctmagent/develop/Servicos/api/app.py` | `/apps/api/app.py` |
| `/ctmagent/develop/Servicos/api/.env.dev` | `/apps/api/.env.dev` |
| `/ctmagent/develop/Servicos/worker/.env` | `/apps/worker/.env` |

A tradução cobre pasta de trabalho, executável/script, interpretador e `.env`.
Os caminhos salvos são os caminhos do contêiner, permitindo recuperar as
aplicações após atualizações. Argumentos que contêm caminhos precisam usar os
caminhos do contêiner.

Para pastas adicionais, monte-as em um override e, se necessário, declare os
aliases explicitamente no `.env` do Compose:

```dotenv
PULSO_PATH_MAPPINGS='[{"host":"/srv/outros-projetos","container":"/outros"}]'
```

```yaml
services:
  pulso:
    volumes:
      - type: bind
        source: /srv/outros-projetos
        target: /outros
        bind:
          create_host_path: false
```

Os aliases não criam volumes: a pasta também precisa estar montada. Caminhos
fora dos volumes disponíveis geram uma orientação de configuração no painel.
Falta de permissão, volume somente leitura e programa/interpretador ausente
também são identificados. O Pulso não altera permissões nem instala dependências
automaticamente.

Se o Pulso precisar criar ou editar arquivos do projeto, dê ao UID `10001` acesso
à pasta dedicada. Por exemplo, após copiar um projeto para ela:

```bash
sudo chown -R 10001:10001 applications/minha-api
```

Em **Nova aplicação**, informe a pasta `/apps/minha-api`, o executável ou script
e, se houver, o caminho `/apps/minha-api/.env`. O arquivo continua opcional.
O fluxo **Editar → Salvar alterações → Reiniciar aplicação** funciona como na
instalação nativa; a gravação altera o arquivo da pasta montada no host.

Serviços web gerenciados precisam escutar em `0.0.0.0` dentro do contêiner.
Para publicar também a porta 8080 de uma aplicação, crie `compose.override.yaml`:

```yaml
services:
  pulso:
    ports:
      - "8080:8080"
```

Depois execute `docker compose up -d`. O mapeamento do painel permanece no
arquivo principal. `localhost` nos programas aponta para o próprio contêiner;
use o endereço acessível do banco ou serviço externo nas variáveis da aplicação.

## Atualizar e recuperar

Para uma instalação que usa a imagem publicada:

```bash
docker compose pull pulso
docker compose up -d --no-deps pulso
docker compose ps
docker compose logs --tail=100 pulso
```

`pull` baixa a imagem; `up -d` recria o contêiner quando a imagem muda. Apenas
`docker compose restart` não troca a imagem. O volume de dados e a pasta de
projetos são reutilizados. Há uma interrupção das aplicações durante a troca;
o Pulso recupera as que estavam ativas e mantém paradas as que foram paradas.
As opções salvas aguardando reinício entram em uso nessa recuperação.

Para fixar uma versão, troque `PULSO_IMAGE` em `.env` para uma tag publicada, por
exemplo `ghcr.io/josehbernardino/pulso:v0.2.0`, e use os mesmos comandos.
O comando `pulso update` não deve ser usado dentro do contêiner: a atualização
é feita pela imagem. Dependências adicionadas manualmente à camada do contêiner
não são uma forma de instalação persistente; use uma imagem personalizada.

O Docker reinicia o serviço após uma falha ou reinicialização do host. Se você
executar `docker compose stop`, `unless-stopped` respeita essa parada; use
`docker compose up -d` para voltar. O health check indica o estado do painel,
mas Docker não reinicia um contêiner apenas porque ele ficou `unhealthy`.
A política responde à saída do processo, conforme a
[referência de serviços do Compose](https://docs.docker.com/reference/compose-file/services/#restart).

## Dependências da aplicação

A imagem padrão atende binários Linux compatíveis, scripts shell, Node.js e
Python. Para Ruby, PHP, compiladores, bibliotecas nativas ou dependências
fixadas do projeto, crie uma imagem derivada. Por exemplo, para uma API Python:

```dockerfile
FROM ghcr.io/josehbernardino/pulso:v0.2.0
USER root
COPY requirements.txt /opt/minha-api/requirements.txt
RUN python3 -m venv /opt/minha-api/venv \
    && /opt/minha-api/venv/bin/pip install --no-cache-dir -r /opt/minha-api/requirements.txt
USER pulso:pulso
```

Compile e publique sua imagem, configure `PULSO_IMAGE` com esse nome e use
`/opt/minha-api/venv/bin/python` no campo **Interpretador** da aplicação. O código
do projeto continua montado em `/apps/minha-api`.

## Operação e backup

```bash
# Estado e logs do serviço Docker
docker compose ps
docker compose logs -f --tail=100 pulso

# Processos gerenciados pelo Pulso
docker compose exec pulso pulso --data-dir /var/lib/pulso list

# Parar ou voltar
docker compose stop pulso
docker compose up -d
```

Para os comandos de uma instalação compilada localmente, mantenha os dois
arquivos `-f compose.yaml -f compose.build.yaml`, como no comando de criação.

Faça backup do volume montado em `/var/lib/pulso` e da pasta de aplicações.
Para uma cópia consistente do SQLite e do estado, pare o serviço durante a cópia.
`docker compose down` preserva os volumes nomeados por padrão; **`down -v` remove
os dados persistentes**. Mantenha o mesmo nome de projeto do Compose e a mesma
pasta de instalação para reutilizar o volume nas atualizações. Se tiver muitos
processos com encerramento lento, ajuste `stop_grace_period` no Compose.

O contêiner não recebe o socket Docker nem privilégios do host. Limites de CPU,
memória e swap podem ser definidos em um override, por exemplo:

```yaml
services:
  pulso:
    cpus: 2
    mem_limit: 2g
    memswap_limit: 2g
    pids_limit: 1024
```

Esses limites são compartilhados entre o Pulso e todas as aplicações no
contêiner. Eles não representam limites individuais de cada processo.

## Verificação para desenvolvimento

```bash
docker build -t pulso:smoke .
python3 scripts/docker-smoke.py pulso:smoke
```

O teste usa somente contêineres e volumes temporários. Valida usuário sem
privilégios, Python/Node.js, health check, login e sessão persistentes, editor de
`.env`, reinício manual, substituição do contêiner e recuperação após SIGKILL,
incluindo a preservação das aplicações que estavam paradas.
