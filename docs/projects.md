# Projetos em contêineres independentes

O modo **Projetos** executa cada aplicação em um contêiner Docker próprio. Cada
projeto tem versão do ambiente, variáveis, portas, limites de memória/CPU e volume
persistente independentes. Atualizar o painel não reinicia esses contêineres.
Os processos já cadastrados continuam disponíveis em **Processos**.

## Instalar sem editar arquivos Compose

Na pasta do checkout, com Docker Engine, Buildx e Compose 2.24.4 ou posterior:

```bash
bash scripts/pulso-docker.sh install
```

O comando compila a imagem, prepara a pasta de aplicações, inicia o painel e o
serviço auxiliar e salva a configuração usada no `.env`. Não exige Go, Node.js
ou Python no servidor. Para projetos em várias pastas do servidor:

```bash
bash scripts/pulso-docker.sh install --host
```

Esse modo dá ao administrador do painel acesso aos arquivos do host. Usa o
override próprio de SELinux e não aplica rótulos `z`/`Z` à raiz `/`.
Para escolher outra porta sem editar o `.env`, acrescente `--port 5010`.

Em uma instalação existente, use:

```bash
git pull --ff-only
bash scripts/pulso-docker.sh start
```

O instalador preserva o projeto Compose, a porta, os volumes e o modo de acesso
existente. Se `COMPOSE_FILE` não estiver salvo, tenta recuperar os arquivos pelos
rótulos do contêiner. Ele acrescenta o gerenciamento de aplicações na ordem
correta e registra a configuração para os próximos comandos.

Para operar depois:

```bash
bash scripts/pulso-docker.sh update
bash scripts/pulso-docker.sh status
bash scripts/pulso-docker.sh logs
bash scripts/pulso-docker.sh doctor
```

`update` faz `git pull --ff-only` em instalações compiladas do checkout, compila
e recria os serviços do painel. Ele recusa alterações locais no código para
evitar sobrescrevê-las. O `.env` de cada aplicação não é alterado.
O socket Unix do Engine é detectado e salvo, inclusive quando o contexto local
seleciona Docker rootless. Contextos TCP/SSH exigem executar o instalador no
servidor do Engine.

Imagens publicadas podem ser selecionadas com `install --registry`. Para também
habilitar a atualização do próprio Pulso pelo painel, use
`install --registry --updates`, depois de publicar a release e as imagens.
Uma imagem privada ou ainda não publicada apresenta o erro e mantém os serviços
atuais; ela não exige autenticação no GitHub para o modo de compilação local.

## Criar um projeto pelo painel

Entre como administrador e abra **Projetos → Novo projeto**. Escolha:

- **Imagem Docker pronta:** informe uma imagem e, se necessário, portas,
  variáveis e pasta persistente. O comando padrão da imagem é preservado.
- **Node.js:** selecione a pasta com `package.json` e a versão 22, 24 ou 26.
  O Pulso instala as dependências e executa o script `build`, quando declarado.
  O comando padrão usa `start`, `serve` ou o arquivo `main`/`index.js`.
- **Python:** selecione a pasta e a versão 3.11, 3.12, 3.13 ou 3.14. O Pulso
  instala `requirements.txt` ou o pacote de `pyproject.toml`, e inicia `app.py`,
  `main.py` ou `server.py`.
- **Dockerfile:** selecione uma pasta com `Dockerfile`; o Buildx usa as etapas,
  dependências e comando definidos pelo projeto. Esse modo atende outros
  runtimes e projetos que precisam de bibliotecas do sistema.

As receitas Node.js e Python executam a aplicação como usuário sem privilégios.
O comando de início pode ser substituído no formulário, com um argumento por
linha. Projetos com frameworks ou serviços auxiliares podem exigir esse comando
ou um Dockerfile adequado. O recurso não interpreta arquivos Compose de projetos
como conjuntos de serviços.

Ao editar um projeto, **Aplicar alterações** recria o contêiner com a imagem
atual e os novos valores de ambiente, recursos ou portas. O código não é
recompilado nessa operação. Marque **Reconstruir imagem com o código da pasta**
para publicar mudanças no código; trocar a origem, pasta ou versão também
dispara uma compilação. Ajustar recursos e variáveis não exige que a pasta
original ainda esteja disponível no servidor.

O contexto admite até 64 MiB e 20000 entradas. `.env`, `.env.*`, `*.env`, `.git`,
`node_modules`, `.venv`, `venv` e `__pycache__` são excluídos. Arquivos especiais
e links simbólicos não são aceitos. O `.dockerignore` é aplicado antes do envio
e da contagem de tamanho, preservando as regras de exclusão e suas exceções.
Nenhuma dependência do host é reutilizada automaticamente.

Carregue um `.env` pelo formulário ou digite as variáveis. Elas são aplicadas ao
contêiner, sem entrar na imagem ou modificar o arquivo original. A porta publicada
automaticamente é registrada para permanecer igual nas próximas implantações.
O programa precisa escutar em `0.0.0.0` na porta interna escolhida; as receitas
recebem `PORT` quando ela não foi informada nas variáveis.

## Dados, recursos e recuperação

Cada projeto recebe, por padrão, limite de 512 MiB de RAM, uma CPU, 256 processos
e logs rotacionados em três arquivos de 10 MiB. O limite de memória e CPU pode
ser ajustado pelo painel. `MemorySwap` é igual ao limite de memória para impedir
swap adicional, quando suportado pelo kernel e pelos cgroups do Docker.
Os limites de execução não limitam a memória/CPU usada pelo BuildKit durante
a compilação. O auxiliar aceita uma implantação de projeto por vez.

Informe a pasta persistente esperada pela imagem para guardar dados em um volume
próprio. As receitas Node/Python preparam `/data`; outras imagens podem usar um
caminho diferente. Excluir o projeto remove seu contêiner e cadastro, mantendo o
volume. A aplicação precisa ter permissão para escrever nesse caminho.

As aplicações usam `unless-stopped`: o Docker restaura as ativas quando seu
daemon volta, e mantém as explicitamente paradas. O serviço Docker precisa estar
configurado para iniciar com o Linux. Reiniciar ou atualizar apenas o painel
mantém os contêineres dos projetos independentes.

Uma implantação baixa ou compila a imagem antes de interromper a versão anterior.
O Pulso verifica o healthcheck da imagem ou dois segundos de execução sem reinícios.
Se o início falhar, tenta reiniciar o contêiner anterior. Isso não desfaz migrações
de banco nem alterações feitas nos dados persistentes. O diagnóstico e os logs
ficam disponíveis no painel. Uma implantação interrompida durante a troca é
reconciliada quando o auxiliar inicia novamente.

Somente administradores podem criar, editar, iniciar, parar, excluir e consultar
configurações ou logs. Usuários de visualização recebem estado e limites, sem
variáveis de ambiente. As operações verificam os rótulos de propriedade antes de
controlar contêineres; não são aceitos volumes do host nem modo privilegiado nos
formulários de projetos. Somente o auxiliar recebe o socket Docker pelo Compose.

## Linux compatível

O caminho de execução é o mesmo em distribuições com Docker Engine compatível,
incluindo as famílias Debian/Ubuntu e RHEL. As imagens publicadas do Pulso atendem
`amd64` e `arm64`. A versão do kernel, os cgroups e as permissões continuam sendo
pré-requisitos do Docker; não há garantia de funcionamento em qualquer sistema
que se identifique como Linux. Consulte as [plataformas do Docker Engine](https://docs.docker.com/engine/install/).
O instalador verifica os requisitos e mostra problemas concretos antes de iniciar.
