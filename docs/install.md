# Instalação

O Pulso roda como um contêiner Docker e implanta cada aplicação em um contêiner
próprio. O instalador não exige checkout do repositório, Go, Node.js, Python nem
Docker Compose.

## Uma linha

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sudo sh
```

Sem `curl`, use `wget -qO- <url> | sudo sh`. O instalador:

1. identifica a distribuição e a arquitetura (x86_64 ou aarch64);
2. instala o Docker Engine quando ausente;
3. detecta SELinux e aplica o rótulo privado `:Z` nos binds, sem desabilitá-lo;
4. confere a hierarquia de cgroups e avisa quando o host ainda usa v1;
5. obtém a imagem (publicada, local ou compilada do código);
6. cria os volumes e a pasta de projetos com a posse correta;
7. sobe o agente privilegiado e o painel, e espera os dois ficarem saudáveis;
8. imprime o endereço e o código de primeiro acesso.

Ao terminar, abra o endereço indicado e crie o administrador com o código exibido.

## Porta e IP no mesmo comando

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sudo sh -s -- --port 8080 --ip 10.0.0.5
```

O `-s --` é o que faz o `sh` entregar as opções ao instalador em vez de
interpretá-las como nome de arquivo. Sem ele, o `sh` tenta abrir `--port` como
um script e nada acontece.

As variáveis de ambiente fazem o mesmo, o que serve melhor para automação:

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | PULSO_PORT=8080 PULSO_BIND=10.0.0.5 sudo -E sh
```

O `-E` preserva as variáveis ao escalar com `sudo`; sem ele o `sudo` as
descarta e o instalador usa os padrões.

`--ip` e `--bind` são a mesma opção, com nomes diferentes para quem pensa em
"o IP" e para quem pensa em "endereço de escuta". O padrão é `0.0.0.0`
(todas as interfaces); informe um endereço privado, ou `127.0.0.1`, para manter
o painel fora da rede.

Executar o comando de novo **atualiza a instalação existente** no lugar. A porta
e o endereço escolhidos ficam gravados em `/etc/pulso/<projeto>.conf`, então
não é preciso repeti-los a cada atualização — informá-los de novo é que muda a
configuração.

## Botão de atualização desabilitado

O botão **Atualizar Pulso** instala imagens publicadas, identificadas por uma
versão de release. Ele exige que o painel e o serviço auxiliar usem
`ghcr.io/josehbernardino/pulso:latest` ou a variante `latest-python311`.
Encontrar uma release no GitHub não significa que a instalação atual atende
a esse requisito.

Uma imagem como `pulso:local` é compilada do código e deve ser atualizada pelo
mesmo instalador usado na máquina. Em uma instalação feita com o instalador de
uma linha, execute-o novamente com `--build` para continuar nesse modo, ou
`--registry` para exigir a imagem publicada. Para uma instalação Compose a
partir de um checkout:

```bash
cd /root/pulso
bash scripts/pulso-docker.sh update
```

Nesse checkout, `bash scripts/pulso-docker.sh start --registry --updates`
seleciona as imagens publicadas e habilita o auxiliar de atualização. O pacote
precisa estar publicado e acessível antes dessa troca. Alterar apenas o nome
da imagem local com `docker tag` não a transforma em uma release publicada.

## Primeiro acesso

Não existe senha padrão. O painel gera um **código de primeiro acesso** no
primeiro arranque, e é ele que cria a conta de administrador. O instalador o
imprime ao terminar:

```
   Painel:  http://10.0.0.5:5011
   Código de primeiro acesso: 7f3c1e9a...
```

Esse código é mostrado uma única vez e sai do histórico do terminal com
facilidade. Para recuperá-lo depois:

```bash
sh get-pulso.sh --token
```

A saída padrão contém **somente** o código, então ele pode ser capturado:

```bash
TOKEN=$(sh get-pulso.sh --token 2>/dev/null)
```

O `--status` também o exibe enquanto estiver pendente. O código vive em
`/var/lib/pulso/security/setup-token`, dentro do contêiner do painel, com
permissão 0600 e dono `10001` — o mesmo usuário sem privilégios que executa o
painel. Ele **deixa de existir** quando a conta de administrador é criada, e a
partir daí `--token` informa isso em vez de devolver vazio.

Se o painel estiver parado, `--token` diz como iniciá-lo em vez de concluir,
erradamente, que a conta já foi criada.

## Projetos fora da pasta de projetos

O painel só enxerga o que está montado nele. Um projeto que vive em outro lugar
do servidor — digamos `/ctmagent/develop/Servicos/minha-api` — aparece como
*"Este caminho não está disponível dentro do contêiner"*. Exponha a pasta de
cima uma única vez:

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sudo sh -s -- --host-root /ctmagent
```

Todos os projetos abaixo dela passam a ficar disponíveis. A montagem é
**somente leitura**: o painel apenas lê o código para empacotá-lo e enviar ao
agente, que compila a imagem.

O caminho é espelhado dentro do contêiner (`/ctmagent` → `/host/ctmagent`), e a
tradução é por prefixo — por isso expor só a subárvore necessária funciona tão
bem quanto expor `/`, e evita dar ao painel visão do servidor inteiro. Se quiser
o servidor todo, use `--host-root /`.

O painel continua rodando sem privilégios. Antes de subir, o instalador prova
que o usuário do contêiner consegue ler a pasta; se não conseguir, ele recusa e
explica as duas saídas em vez de deixar o erro aparecer depois, no painel, com a
mesma mensagem de "caminho indisponível":

- dar permissão de leitura a outros na árvore (`chmod o+rX`);
- ou `--host-root-privileged`, que sobe o painel como root com `DAC_OVERRIDE`.
  Use somente em servidor de confiança.

A escolha fica gravada em `/etc/pulso/<projeto>.conf`; reexecutar o instalador
a preserva.

## Distribuições validadas

O workflow [Installer](../.github/workflows/installer.yml) executa o instalador
em cada uma destas imagens a cada alteração:

| Família | Distribuições | Instalação do Docker |
| --- | --- | --- |
| Debian | Debian, Ubuntu, Mint, Pop!_OS, Zorin, Kali, Raspbian, Devuan | script oficial da Docker |
| RHEL | RHEL, CentOS, **Rocky**, **AlmaLinux**, Oracle, Fedora, CloudLinux | script oficial da Docker |
| Amazon | Amazon Linux 2 e 2023 | `amazon-linux-extras` / `dnf` |
| SUSE | SLES, SLED, openSUSE Leap e Tumbleweed | `zypper` |
| Arch | Arch, Manjaro, EndeavourOS, Garuda | `pacman` |
| Alpine | Alpine | `apk` |

Distribuições derivadas são reconhecidas por `ID_LIKE` no `/etc/os-release`.

### Notas por família

- **RHEL 8 e superiores:** o pacote `podman-docker` ocupa `/usr/bin/docker` e
  impede o Docker Engine. O instalador remove **apenas** esse pacote; o `podman`
  permanece instalado.
- **SELinux (RHEL, Fedora, Rocky, AlmaLinux):** o instalador usa `:Z`, que
  rotula o conteúdo do bind como privado do contêiner. Não é necessário, nem
  recomendado, desabilitar o SELinux. O pacote `container-selinux` é instalado
  quando ausente.
- **Amazon Linux:** a Docker não publica repositório para essa distribuição. O
  pacote nativo não traz os plugins `buildx` e `compose`; o modo `--build`
  precisa do `buildx`.
- **cgroups v1** (Amazon Linux 2, CentOS 7, Debian 10): os limites por
  contêiner funcionam. Para a hierarquia unificada, acrescente
  `systemd.unified_cgroup_hierarchy=1` aos parâmetros do kernel e reinicie.

## Opções

```
--port <n>            Porta publicada (padrão 5011)
--bind <endereço>     Endereço de escuta; 127.0.0.1 expõe só localmente
--ip <endereço>       O mesmo que --bind
--registry            Exige a imagem publicada
--local               Usa uma imagem já presente no host, sem acessar a rede
--build               Compila a imagem a partir do código
--image <ref>         Repositório da imagem
--tag <tag>           Tag da imagem
--no-docker-install   Não instala o Docker
--open-firewall       Libera a porta no firewalld ou ufw ativo
--apps-dir <path>     Pasta de projetos no host
--host-root <path>    Expõe uma pasta do servidor no painel, somente leitura
--host-root-privileged  Sobe o painel como root para ler árvores fechadas
--project <nome>      Nome da instância (permite várias no mesmo host)
--status              Mostra o estado da instalação
--token               Mostra o código de primeiro acesso ainda pendente
--uninstall           Remove os contêineres do Pulso, preservando os dados
--purge               Remove contêineres e volumes (destrutivo)
--dry-run             Mostra o que seria feito, sem executar
```

Exemplos:

```bash
# Painel acessível apenas na própria máquina, em outra porta
curl -fsSL <url> | sudo sh -s -- --port 8080 --ip 127.0.0.1

# Rede fechada: imagem carregada antes com `docker load`
sh get-pulso.sh --local --image minha-registry.interna/pulso --tag 1.4.0

# Servidor sem acesso ao registro público: compila do código
sh get-pulso.sh --build
```

## Rede fechada e air-gapped

Em redes sem saída para o `ghcr.io`, transfira a imagem e instale com `--local`:

```bash
# em uma máquina com acesso
docker pull ghcr.io/josehbernardino/pulso:latest
docker save ghcr.io/josehbernardino/pulso:latest | gzip > pulso.tar.gz

# no servidor de destino
gunzip -c pulso.tar.gz | docker load
sh get-pulso.sh --local --no-docker-install
```

No modo automático, a precedência é: imagem publicada, imagem já presente no
host, compilação local. Um registro inacessível não interrompe a instalação
quando a imagem já está no servidor.

## Atualizar

Execute o instalador de novo. Ele recupera porta, endereço, pastas e imagem do
arquivo gravado em `/etc/pulso/<projeto>.conf`, recria o painel e o agente, e
**não reinicia os contêineres das aplicações**.

```bash
curl -fsSL <url> | sudo sh
```

## Várias instâncias no mesmo host

Use `--project` com portas distintas. Cada instância tem contêineres, volumes,
rede de aplicações e arquivo de configuração próprios:

```bash
sh get-pulso.sh --project producao  --port 5011
sh get-pulso.sh --project homologa  --port 5012
```

## O que o instalador cria

| Recurso | Nome padrão |
| --- | --- |
| Contêiner do painel | `pulso-panel` |
| Contêiner do agente | `pulso-updater` |
| Volume de dados | `pulso_data` |
| Volume do socket do agente | `pulso_updates` |
| Rede das aplicações | `pulso-applications` (criada na primeira implantação) |
| Pasta de projetos | `/var/lib/pulso/applications` |
| Configuração | `/etc/pulso/pulso.conf` |

### Separação de privilégio

O painel **não** recebe o socket do Docker. Ele roda sem privilégios, com
sistema de arquivos somente leitura e todas as capabilities removidas. Só o
agente acessa o Engine, e os dois conversam por um socket Unix em um volume
compartilhado, montado somente para leitura no painel.

Isso difere do Portainer, que monta o socket do Docker no próprio contêiner da
interface. Uma falha na interface do Pulso não alcança o Engine.

Os contêineres de infraestrutura levam o rótulo `io.pulso.infrastructure=1`,
para que a gestão de contêineres pelo painel não possa pará-los.

> **Acoplamento com o código:** o agente se localiza pelos rótulos
> `com.docker.compose.project` e `com.docker.compose.service`, aplicados pelo
> instalador sem usar o Compose (ver `internal/containerupdate/agent.go`). A rede
> das aplicações é derivada de `<projeto>-applications`. Mudar `--project` em uma
> instalação existente órfã a rede e os contêineres anteriores.

## Remover

```bash
sh get-pulso.sh --uninstall   # remove o painel e o agente; preserva os dados
sh get-pulso.sh --purge       # remove também contas, cadastros e histórico
```

As aplicações implantadas continuam em execução depois de `--uninstall`: elas
são contêineres independentes. A pasta de projetos no host nunca é removida.

## Diagnóstico

```bash
sh get-pulso.sh --status
docker logs -f pulso-panel
docker logs -f pulso-updater
```

| Sintoma | Causa provável |
| --- | --- |
| `Socket do Docker não encontrado` | Engine parado, ou contexto TCP/SSH ativo. O agente precisa do socket Unix local. |
| `O daemon do Docker não respondeu` | `systemctl status docker`; em WSL ou contêineres sem systemd, inicie o daemon manualmente. |
| `Imagem publicada indisponível` | Release ainda não publicada. Use `--build` ou `--local`. |
| `O modo de compilação exige o plugin Docker Buildx` | O Dockerfile usa `--mount=type=cache`, que exige BuildKit. O instalador tenta instalar o plugin; em Amazon Linux instale-o à mão ou use `--registry`. |
| Painel inacessível de outra máquina | Firewall. Use `--open-firewall`, ou libere a porta no firewalld/ufw. |

## Compose

Os arquivos Compose continuam disponíveis e suportados para quem prefere
gerenciar a pilha dessa forma; veja o [guia Docker](docker.md) e o
[guia de projetos](projects.md). O instalador de uma linha dispensa o Compose
justamente porque a versão do plugin varia muito entre distribuições.
