# Gestão de recursos do Docker

O painel lista e administra **todos** os recursos do Engine, inclusive os que
o Pulso não criou: contêineres de outras pilhas, imagens, volumes e redes.
É o que cobre o uso que normalmente exigiria o Portainer ao lado.

A diferença está em duas coisas: o painel nunca toca o Engine diretamente, e
há recursos que ele se recusa a alterar.

---

## Separação de privilégio

O contêiner do painel **não recebe o socket do Docker**. Ele roda sem
privilégios, com sistema de arquivos somente leitura e todas as capabilities
removidas. Só o agente acessa o Engine, e os dois conversam por um socket Unix
em um volume compartilhado, montado somente para leitura no painel.

Isso difere do Portainer, que monta o socket do Docker no próprio contêiner da
interface — ali, uma falha na interface é root no host.

Toda verificação de propriedade acontece **no agente**, não no painel. Mesmo
que o painel peça para parar o agente, o agente recusa.

---

## Classificação de propriedade

Cada contêiner é classificado antes de qualquer ação:

| Classe | O que é | Ações permitidas |
| --- | --- | --- |
| `infrastructure` | O painel e o agente desta instalação | **nenhuma** |
| `application` | Um projeto implantado pelo Pulso | iniciar, parar, reiniciar |
| `foreign` | Qualquer outra coisa no Engine | todas |

**Por que a infraestrutura é intocável:** parar o agente deixaria o painel sem
como gerenciar nada, inclusive sem como religar o agente.

**Por que uma aplicação não pode ser removida aqui:** removê-la por fora
deixaria o cadastro do projeto órfão. Use a página **Projetos**, que remove o
contêiner e o cadastro juntos. Parar e iniciar são permitidos, porque o estado
exibido é lido do Docker e continua correto.

A classificação usa o rótulo `io.pulso.infrastructure=1` e, como alternativa
para instalações anteriores a ele, o par de rótulos
`com.docker.compose.project` + `com.docker.compose.service`. Uma segunda
instalação do Pulso no mesmo host é `foreign` para esta — cada uma protege
apenas os próprios recursos.

### Recursos protegidos

| Recurso | Protegido quando |
| --- | --- |
| Contêiner | É o painel, o agente, ou um projeto do Pulso (só remoção) |
| Imagem | É a imagem do painel ou do agente, ou está em uso |
| Volume | Tem o rótulo de infraestrutura, é `<projeto>_data` ou `<projeto>_updates`, pertence a um projeto, ou está em uso |
| Rede | É predefinida (`bridge`, `host`, `none`), é a rede dos projetos, pertence à instalação, ou tem contêineres conectados |

A proteção de volumes e redes é primariamente **por rótulo**, então continua
valendo se a instalação for renomeada ou o recurso tiver outro nome. A
comparação por nome existe como rede de segurança para instalações antigas.

---

## Permissões

| Operação | Papel exigido | Por quê |
| --- | --- | --- |
| Listar contêineres, imagens, volumes, redes, disco | `view` | As listagens do Docker não trazem o ambiente, então não vazam credencial |
| Inspecionar um contêiner | `view`, **com redação** | O inspect devolve o ambiente; para quem não é administrador as chaves ficam e os valores saem (`CHAVE=***`), e o caminho de origem dos binds é omitido |
| Ler logs de um contêiner | `admin` | Um programa pode imprimir segredo no próprio log |
| Iniciar, parar, reiniciar, remover | `admin` | — |
| Remover imagem, volume, rede | `admin` | — |
| Limpeza de disco | `admin` + confirmação | Apaga dados |

Toda alteração entra no [registro de auditoria](observability.md) com ação,
alvo, autor e resultado — inclusive quando é recusada.

---

## API

```
GET    /api/v1/docker/containers
GET    /api/v1/docker/containers/{id}
GET    /api/v1/docker/containers/{id}/logs?tail=200
POST   /api/v1/docker/containers/{id}/{start|stop|restart|remove}
GET    /api/v1/docker/images
DELETE /api/v1/docker/images/{id}
GET    /api/v1/docker/volumes
DELETE /api/v1/docker/volumes/{name}
GET    /api/v1/docker/networks
DELETE /api/v1/docker/networks/{id}
GET    /api/v1/docker/disk
POST   /api/v1/docker/prune
```

Cada item das listagens traz `protected` e, quando protegido, o
`protect_reason` — a interface pode desabilitar o botão e explicar o motivo em
vez de deixar o usuário descobrir pelo erro.

---

## Limpeza de disco

Servidores de deploy morrem de disco cheio. A limpeza remove o que não está
em uso, respeitando todas as proteções.

```bash
# simular: nada é apagado
curl -X POST .../api/v1/docker/prune \
  -d '{"resources":["images","volumes","build-cache"],"dry_run":true}'

# executar: exige confirmação explícita
curl -X POST .../api/v1/docker/prune \
  -d '{"resources":["images","volumes","build-cache"],"confirm":true}'
```

Sem `dry_run` e sem `confirm`, o pedido é recusado.

### O que entra na limpeza

| Recurso | Critério |
| --- | --- |
| `containers` | Parados; nunca em execução, reiniciando ou pausados |
| `images` | **Apenas sem tag** (órfãs) e sem contêiner |
| `volumes` | Sem referência de contêiner e não protegidos |
| `networks` | Sem contêineres conectados e não protegidas |
| `build-cache` | Registros não usados e não compartilhados |

**Imagens com tag nunca são removidas.** Elas foram baixadas ou construídas de
propósito; apagá-las forçaria um download novo a cada implantação.

### Por que a simulação é confiável

O prune do Docker é tudo-ou-nada e não consegue honrar estas proteções. Em vez
de usá-lo, o Pulso calcula os candidatos e remove um por um. A consequência é
que a simulação lista exatamente o que a execução removeria, e o número de
bytes exibido é o que será liberado.

A resposta traz três listas distintas:

- `candidates` — o que seria removido;
- `removed` e `failed` — o que de fato saiu e o que o Engine recusou, item por
  item (uma falha não aborta o resto);
- `skipped` — o que foi deliberadamente preservado, com o motivo.

O valor de `reclaimable` em `GET /docker/disk` vem desses mesmos candidatos,
então o número do painel é o número real.
