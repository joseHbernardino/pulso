# Versão e atualização pelo painel

A versão instalada aparece no cabeçalho das telas de aplicações e usuários e
no rodapé do login. Clique na versão para ver a data de compilação e o código
que originou a imagem. Compilações sem uma versão de release mostram `dev` e,
quando informado na compilação, o identificador do commit.

Administradores podem verificar publicações e atualizar pelo portal. Usuários
com permissão de visualização continuam vendo a versão instalada.

## Ativar uma vez no Docker

A instalação precisa usar uma imagem publicada que já contenha este recurso.
O workflow `Container` publica imagens ao enviar uma tag de release `vX.Y.Z`;
um commit na `main` sozinho não publica uma nova imagem. O verificador consulta
a última release estável do GitHub. A publicação da release e da imagem precisam
estar concluídas antes da instalação pelo painel.

Na `.env` do Compose, mantenha a imagem na tag de acompanhamento apropriada:

```dotenv
PULSO_IMAGE=ghcr.io/josehbernardino/pulso:latest
# Para aplicações que exigem Python 3.11:
# PULSO_IMAGE=ghcr.io/josehbernardino/pulso:latest-python311
```

Na mesma pasta e com o mesmo nome de projeto da instalação existente:

```bash
docker compose -f compose.yaml -f compose.update.yaml up -d --pull always
```

Se usa acesso completo ao host, acrescente o arquivo de atualizações **por último**:

```bash
docker compose -f compose.yaml -f compose.host.yaml -f compose.update.yaml up -d --pull always
```

Se utiliza `COMPOSE_FILE` na `.env`, atualize-o com os mesmos arquivos, na mesma
ordem. Remova `compose.build.yaml` dessa lista: ele seleciona `pulso:local`, que
precisa ser compilada pelo Compose. Imagens locais e tags fixas continuam
mostrando a versão; o painel explica por que a atualização automática não está
habilitada para elas. As tags `latest` e `latest-python311` permitem acompanhar
novas versões sem editar a configuração a cada publicação.

Esse comando reutiliza os volumes existentes. Preserve as configurações de
portas e pastas; não remova o volume `pulso_data` ao migrar.

O override acrescenta o serviço `pulso-updater`, sem porta TCP publicada. Ele se
comunica com o portal por um socket Unix em um volume dedicado. Apenas esse
auxiliar e o processo temporário de atualização recebem acesso ao socket Docker.
Esse acesso permite administrar o Docker e equivale a privilégios elevados no
host, como explica a [documentação de segurança do Docker](https://docs.docker.com/engine/security/protect-access/).
O auxiliar aceita somente a última release estável do repositório configurado e
atua nos serviços `pulso` e `pulso-updater` do seu próprio projeto Compose.

## Usar no portal

1. Entre como administrador e clique na versão no cabeçalho.
2. Use **Verificar atualização** para consultar a última release estável.
3. Clique em **Atualizar para…** e confira a versão exibida.
4. Confirme a atualização e acompanhe o progresso. O portal se reconecta após
   o reinício e recarrega a interface ao concluir.

O painel também verifica publicações ao abrir e periodicamente enquanto estiver
em uso. Consultas ao GitHub usam cache no auxiliar. Falhas de rede ou de acesso
ao registro aparecem no painel; o download ocorre antes de interromper os serviços.

O fluxo salva o estado desejado das aplicações, baixa as imagens, confere a
versão declarada e cria os novos contêineres pelo ID imutável da imagem. Mantém
volumes, portas, redes, variáveis e opções do contêiner. Confere o health check
do Pulso e os estados dos processos: aplicações antes ativas devem estar em
execução e prontas; aplicações pausadas devem continuar paradas. Depois atualiza
o próprio auxiliar e verifica seu health check.

Se uma etapa falhar, tenta restaurar os contêineres anteriores e registra o
motivo. Um processo separado executa a troca, inclusive se o navegador for fechado.
O auxiliar também tenta recuperar uma atualização interrompida. Falhas que impeçam
a própria recuperação são apresentadas como erro e exigem conferir os logs.
Durante a atualização, alterações pelo portal ficam bloqueadas; evite alterar
configurações por CLI nesse intervalo.

O retorno à imagem anterior **não desfaz mudanças nos arquivos e bancos dos
volumes**. Faça backup dos dados antes de versões que alterem o formato desses
arquivos ou executem migrações. Imagens anteriores permanecem no cache do Docker;
o fluxo não remove imagens que possam ser usadas por outras instalações.

## Imagens próprias e registros privados

Se suas aplicações exigem bibliotecas adicionais, publique uma imagem própria
que preserve o binário, os comandos e o health check do Pulso. Não substitua essa
imagem pela oficial ao atualizar: as dependências devem fazer parte de cada
versão publicada. Configure os destinos correspondentes na `.env`:

```dotenv
PULSO_IMAGE=ghcr.io/minha-organizacao/pulso:latest
PULSO_UPDATE_IMAGE=ghcr.io/minha-organizacao/pulso
PULSO_RELEASE_REPOSITORY=minha-organizacao/pulso
```

As imagens precisam de tags `vX.Y.Z` e `latest` (com sufixo `-python311` quando
aplicável), metadados `org.opencontainers.image.version` e da versão injetada no
binário. O Dockerfile e o workflow do projeto já fazem isso. O auxiliar usa a
mesma variante da imagem instalada.

Imagens públicas não exigem credenciais. Para imagens privadas, use um arquivo
Docker dedicado com credenciais inline (`auth` ou `identitytoken`). Por exemplo,
`docker --config /opt/pulso-registry login ghcr.io` cria o arquivo após o login.
Um arquivo que dependa de `credsStore`/`credHelpers` externos não é suficiente.
Monte-o somente no auxiliar por um override adicional:

```yaml
services:
  pulso-updater:
    environment:
      PULSO_REGISTRY_CONFIG: /run/pulso-registry/config.json
    volumes:
      - type: bind
        source: /opt/pulso-registry/config.json
        target: /run/pulso-registry/config.json
        read_only: true
```

As credenciais não são devolvidas pelo portal nem incluídas no progresso público.
O socket do Docker rootless pode ser selecionado por `PULSO_DOCKER_SOCKET` na
`.env` do Compose. O recurso gerencia uma instalação Docker Compose com um único
contêiner de cada serviço; Swarm e Kubernetes usam seus próprios mecanismos de atualização.

## Conferir a operação

```bash
docker compose -f compose.yaml -f compose.update.yaml ps
docker compose -f compose.yaml -f compose.update.yaml logs --tail=100 pulso-updater
```

Mantenha os overrides usados na instalação, inclusive `compose.host.yaml` quando
aplicável. O progresso e o último resultado são persistidos no volume
`pulso_updates`. Os contêineres temporários não executam trabalho após concluir;
o anterior é removido na próxima atualização. Contêineres de backup ficam
parados e com reinício desabilitado até a verificação ser concluída.

Para validar em desenvolvimento, depois de compilar `pulso:smoke`:

```bash
bash scripts/docker-update-smoke.sh pulso:smoke
```

Esse teste requer root ou `sudo` sem senha. O script eleva somente a execução do
teste Go, que cria e lê arquivos privados pertencentes ao UID 10001 do contêiner.
As compilações continuam no usuário que chamou o script, preservando o builder
Buildx selecionado. O [runner Ubuntu do GitHub Actions](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#administrative-privileges)
já oferece esse `sudo`.
Esse requisito é do teste de integração; o portal continua executando como
usuário `pulso` dentro do contêiner.

O teste usa fixtures descartáveis para substituir o Pulso e o auxiliar, verificar
versão, sessão, porta, aplicações ativas/pausadas e provocar o retorno automático
após uma imagem que encerra com erro, uma dependência ausente e a interrupção
do processo de atualização. A consulta à release e o download são
substituídos por imagens locais no teste; a troca usa a API real do Docker.

Para reproduzir o isolamento do builder usado no GitHub Actions, execute com o
driver `docker-container` (Go e o plugin Buildx precisam estar disponíveis):

```bash
docker buildx create --name pulso-ci-check --driver docker-container
docker buildx build --builder pulso-ci-check --load -t pulso:smoke .
BUILDX_BUILDER=pulso-ci-check bash scripts/docker-update-smoke.sh pulso:smoke
docker buildx rm pulso-ci-check
```

As imagens de teste derivam do estágio `runtime` dentro da própria definição de
compilação. Assim, o builder isolado resolve as etapas sem precisar encontrar
tags locais do daemon em um registro externo.
