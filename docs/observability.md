# Métricas, auditoria e notificações

Três recursos que respondem, respectivamente: *quanto cada aplicação está
consumindo*, *quem mudou o que*, e *como ser avisado sem ficar olhando o
painel*.

Todos funcionam em rede fechada: nada precisa ser alcançável de fora.

---

## Métricas por contêiner

Cada aplicação reporta consumo ao vivo, nos mesmos termos do `docker stats`.

```
GET /api/v1/container-stats                       # todas, em uma chamada
GET /api/v1/container-applications/{id}/stats     # uma aplicação
```

Resposta por aplicação:

| Campo | Significado |
| --- | --- |
| `cpu_percent` | Percentual de CPU, com `cpus * 100` como teto |
| `cpus` | CPUs vistas pelo Engine, base do percentual |
| `memory_bytes` | Memória em uso, **descontado o cache de página** |
| `memory_limit` | Limite configurado, em bytes |
| `memory_percent` | `memory_bytes / memory_limit` |
| `network_rx_bytes`, `network_tx_bytes` | Acumulado, somando todas as interfaces |
| `block_read_bytes`, `block_write_bytes` | I/O de disco acumulado |
| `processes` | Processos dentro do contêiner |
| `running` | Falso para aplicação parada ou ainda sem contêiner |
| `error` | Preenchido apenas na entrada que falhou |

Detalhes que importam:

- **O cache de página é descontado.** Memória de cache é reclamável pelo kernel
  e não é consumo da aplicação. O contador tem nome diferente por versão de
  cgroups — `inactive_file` na v2, `total_inactive_file` na v1 hierárquica,
  `cache` na v1 simples — e os três são tratados.
- **Uma amostra basta.** Com `stream=false` o Engine preenche `precpu_stats`
  com a leitura anterior, o que permite calcular o percentual de CPU sem
  manter conexão aberta.
- **Falha isolada.** Na listagem em lote, uma aplicação com problema reporta o
  erro na própria entrada, preservando o nome, e não derruba as demais.
- **Coleta limitada.** No máximo 6 requisições simultâneas ao Engine, para uma
  instalação grande não inundar o socket.
- **Leitura é liberada ao papel `view`.** Métricas não revelam variáveis nem
  configuração. Escrever continua restrito ao administrador.

As métricas são instantâneas, não históricas. Séries temporais com gráficos
são um item separado do roteiro.

---

## Registro de auditoria

Quem fez o quê, incluindo **o que foi recusado**.

```
GET /api/v1/audit?actor=&action=&hours=&limit=     # somente administrador
```

| Campo | Conteúdo |
| --- | --- |
| `actor`, `role` | Conta e papel; `anônimo` quando não havia sessão |
| `action` | Nome estável, como `application.deploy` ou `auth.login` |
| `target` | Alvo da ação: identificador, nome da aplicação, conta tentada |
| `outcome` | `ok`, `denied` (401/403) ou `error` (demais ≥ 400) |
| `status` | Código HTTP |
| `detail` | Nota curta do handler, sempre livre de segredos |
| `ip`, `user_agent` | Endereço do par e cliente |

O filtro de `action` casa por prefixo: `application.` seleciona a família
inteira. `command.exec` registra cada execução do console e do endpoint
`/api/v1/exec`, inclusive as recusadas por papel; `command.exec.stop` registra
quem interrompeu um comando em andamento. Veja
[comandos e scripts shell](shell-commands.md).

### O que garante que não há segredo no registro

- **O corpo da requisição nunca é lido pela auditoria.** Um corpo pode conter
  senha ou variável de ambiente. Só entram o método, o padrão da rota, o
  status e o que o handler relata explicitamente.
- **Variáveis de ambiente entram por nome, nunca por valor.** Uma implantação
  registra `variáveis: API_KEY, DATABASE_URL` — as chaves, em ordem.
- **O IP é o do par, não o de cabeçalho.** `X-Forwarded-For` é controlado por
  quem chama; um registro de auditoria não pode ser forjável.
- **O comando do console entra com os valores atribuídos substituídos.** Uma
  execução registra `command.exec` com a primeira linha do comando, e todo
  trecho `NOME=valor` vira `NOME=***` — a linha de comando é um dos poucos
  lugares em que alguém digita uma senha direto (`PGPASSWORD=… pg_dump`).

### Abrangência

O registro envolve a autenticação e a autorização, então uma tentativa
recusada por sessão ausente (401) ou por papel (403) também é registrada — é o
que permite notar varredura de endpoints sem credencial.

Apenas requisições que alteram estado são registradas (POST, PUT, PATCH,
DELETE). Leituras encheriam o registro sem acrescentar prestação de contas.

Rotas novas, ainda sem nome amigável, são registradas pelo padrão da rota: a
auditoria não depende de alguém lembrar de cadastrar a ação.

### Armazenamento

Arquivo SQLite próprio em `<dados>/security/audit.sqlite`, modo `0600`,
separado do banco de contas — um problema em um não derruba o outro. A
retenção padrão é de 90 dias, aplicada no início do daemon.

---

## Notificações

Avisos por **tráfego de saída apenas**: funcionam atrás de qualquer firewall,
sem nada exposto.

### Canais

| Tipo | Configuração obrigatória | Credencial |
| --- | --- | --- |
| `slack` | `webhook_url` (https) | `webhook_url` |
| `discord` | `webhook_url` (https) | `webhook_url` |
| `telegram` | `bot_token`, `chat_id` | `bot_token` |
| `gotify` | `url`, `token` | `token` |
| `ntfy` | `url`, `topic` | `token` (opcional) |
| `webhook` | `url` | `url`, `authorization` |
| `email` | `host`, `from`, `to` | `password` |

Gotify, ntfy, webhook e e-mail aceitam `http://` e endereços internos, porque
um serviço self-hosted costuma não ter certificado. Slack e Discord exigem
`https://`: aceitar `http` neles só vazaria o segredo do webhook.

### Eventos

`deploy.succeeded`, `deploy.failed`, `application.stopped`,
`application.unhealthy`, `system.update`, `resource.threshold`.

Lista de assinatura vazia significa **todos os eventos**.

### API

```
GET    /api/v1/notifications/channels            # somente administrador
POST   /api/v1/notifications/channels
PUT    /api/v1/notifications/channels/{id}
DELETE /api/v1/notifications/channels/{id}
POST   /api/v1/notifications/channels/{id}/test
```

Exemplo, ntfy interno avisando apenas falhas:

```json
{
  "kind": "ntfy",
  "name": "Plantão",
  "enabled": true,
  "events": ["deploy.failed", "application.unhealthy"],
  "settings": { "url": "http://ntfy.interno", "topic": "pulso" }
}
```

### Credenciais

A API **nunca** devolve uma credencial: ela é substituída pelo marcador
`__stored__`. Um campo vazio continua vazio, para a interface distinguir "não
configurado" de "configurado e oculto". Salvar de volta com o marcador
preserva o valor guardado, então editar um canal sem redigitar o segredo não o
apaga.

Gerenciar canais é restrito ao administrador, **inclusive para ler** — a
configuração contém credenciais.

### Entrega

- **Nunca bloqueia a operação.** `Dispatch` enfileira e retorna. Uma
  notificação não pode atrasar uma implantação.
- **Fila limitada** em 256 eventos. Cheia, o evento é descartado e contado; o
  aviso no log é limitado a 1 em cada 100 para um pico não inundá-lo.
- **3 tentativas** com espera crescente e 10 s de prazo cada.
- **O teste manual faz uma tentativa só**, com 8 s de prazo: alguém está
  esperando a resposta, e um erro claro vale mais que três tentativas silenciosas.
- **Desligamento limitado.** O `Close` dá 5 s para entregar o que está na
  fila e então aborta o que resta. Uma notificação pendente nunca prende o
  desligamento do daemon.
- **O resultado da implantação é acompanhado.** A implantação é assíncrona: o
  painel segue a aplicação até `ready` ou `failed` e então notifica, com
  limite de 15 minutos para não vazar goroutine.

Injeção de cabeçalho é bloqueada: quebras de linha são removidas de títulos
que entram em cabeçalhos HTTP ou de e-mail, e remetente e destinatários são
validados.

### Armazenamento

Arquivo JSON em `<dados>/security/notifications.json`, modo `0600`, escrito
atomicamente — uma falha no meio da escrita preserva a configuração anterior
em vez de deixar um arquivo truncado. São poucos registros lidos no momento da
entrega; não precisam de banco.
