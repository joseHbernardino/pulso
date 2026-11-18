# Pulso — Catálogo de opções para fechar a lacuna com o Dokploy

> Documento de **escolha**, não de implementação. Marque as opções desejadas
> (`[x]`) e eu detalho o plano técnico completo de cada uma escolhida:
> arquivos, schema, rotas, testes, migração e documentação.
>
> Gerado em 2026-11-17 a partir da análise de `/root/pulso` (Go, 246 arquivos,
> ~36.900 LOC, 27 pacotes internos, 77 arquivos de teste) e `/root/dokploy`
> (TypeScript/Next.js, 57 routers tRPC, 54 tabelas, 204 migrações, 191 testes).

---

## 1. Diagnóstico: onde o Pulso já está bom

Isto **não** precisa ser refeito. É a base sobre a qual as opções abaixo se encaixam.

| Área | Estado no Pulso |
| --- | --- |
| Supervisão de processos | Forte — 4.365 LOC, backoff exponencial, políticas de restart, reload graceful |
| Multi-runtime | Go, Python, Node, Bun, Deno, Ruby, PHP com autodetecção |
| Interfaces | CLI (39 comandos), TUI, Web UI, MCP — nenhuma concorrente tem as 4 |
| Deploy em contêiner | `internal/containerupdate` (2.913 LOC): imagem pronta, Node, Python, Dockerfile, com build via Buildx |
| Troca sem downtime | Sobe candidato → healthcheck/2s estável → troca → rollback se falhar |
| Auth | SQLite, sessões, bcrypt, papéis `admin`/`view`, setup code inicial |
| Healthcheck | HTTP, TCP e comando |
| Métricas | CPU/mem/threads/FD por processo + export Prometheus |
| Segredos | Resolver com referências externas (`internal/secrets`) |
| Agendamento | Cron para processos e tarefas |
| Hooks | pre/post start, stop, restart, reload |
| Empacotamento | Docker multi-stage, GoReleaser, Flake Nix, módulo NixOS, Home Manager |
| CI | 3 workflows (ci, container, release) + Dependabot |
| Documentação | 20 guias em `docs/` — densidade acima da média |
| Hardening do contêiner | `read_only`, `cap_drop: ALL`, `no-new-privileges`, tmpfs |

**Conclusão:** o Pulso é um **supervisor de processos excelente** com um deploy de
contêineres funcional. O Dokploy é uma **PaaS**. A diferença não é qualidade de
código — é superfície de produto. As opções abaixo são exatamente essa diferença.

---

## 2. Matriz de lacunas (resumo executivo)

| # | Capacidade | Pulso | Dokploy | Impacto |
| --- | --- | :-: | :-: | --- |
| A1 | Reverse proxy + domínios + TLS automático | ✗ | ✓ Traefik + Let's Encrypt | **Crítico** |
| A2 | Deploy a partir de Git (GitHub/GitLab/Gitea) | ✗ | ✓ | **Crítico** |
| A3 | Webhook de deploy / CI push-to-deploy | ✗ | ✓ | **Crítico** |
| A4 | Histórico de deploys + rollback por versão | parcial | ✓ `rollbacks` | **Alto** |
| B1 | Bancos gerenciados (Postgres, MySQL, Mongo, Redis…) | ✗ | ✓ 7 engines | **Alto** |
| B2 | Backups agendados + restore (S3) | ✗ | ✓ | **Alto** |
| B3 | Backup/restore de volumes | ✗ | ✓ | Médio |
| C1 | Suporte a `docker-compose.yml` do projeto | ✗ (recusa) | ✓ | **Alto** |
| C2 | Catálogo de templates one-click | ✗ | ✓ | Médio |
| C3 | Build packs (Nixpacks, Buildpacks, Railpack, static) | ✗ | ✓ 8 builders | Médio |
| D1 | Multi-servidor via SSH | ✗ | ✓ | **Alto** |
| D2 | Docker Swarm / réplicas / cluster | ✗ | ✓ | Médio |
| E1 | Notificações (Slack, Discord, Telegram, e-mail, webhook…) | ✗ | ✓ 12 canais | **Alto** |
| E2 | Métricas históricas + gráficos no painel | ✗ (instantâneo) | ✓ | **Alto** |
| E3 | Terminal/exec no contêiner pelo navegador | ✗ | ✓ | Médio |
| E4 | Log de auditoria | ✗ | ✓ `audit-log` | Médio |
| F1 | Tokens de API + OpenAPI | ✗ | ✓ openapi.json | **Alto** |
| F2 | 2FA / SSO / SCIM | ✗ | ✓ | Baixo (self-host solo) |
| F3 | Organizações, times, permissões por projeto | ✗ (2 papéis globais) | ✓ | Médio |
| G1 | Migrações de schema versionadas do SQLite | parcial | ✓ 204 migrações | **Alto** |
| G2 | i18n (hoje strings PT-BR fixas no código Go) | ✗ | ✓ | **Alto** |
| G3 | Preview deployments por PR | ✗ | ✓ | Baixo |
| G4 | Limpeza de disco / GC de imagens | ✗ | ✓ | Médio |
| G5 | Provedores de DNS + gestão de certificados | ✗ | ✓ 5 provedores | Baixo |
| G6 | Export/import de projeto | ✗ | ✓ | Baixo |

---

## 3. As opções

Cada opção traz: **o que é**, **por que importa**, **escopo técnico**,
**esforço** (P ≤ 2 dias, M ≈ 1 semana, G ≈ 2–4 semanas) e **dependências**.

### Grupo A — Entrada de tráfego e deploy (o que mais falta)

#### [ ] A1. Reverse proxy embutido com domínios e HTTPS automático
**O que é:** cada aplicação ganha um ou mais domínios (`api.seudominio.com`),
com certificado Let's Encrypt renovado automaticamente, redirect HTTP→HTTPS,
path prefix e, opcionalmente, basic auth.

**Por que importa:** hoje uma app no Pulso só é alcançável por `host:porta`.
Sem isto, não existe produção. É **a** lacuna que separa "gerenciador de
processos" de "plataforma de deploy".

**Escopo técnico sugerido:**
- Novo pacote `internal/proxy` com duas estratégias atrás de uma interface:
  - **Opção 1 (recomendada):** Traefik como contêiner irmão, configurado por
    *labels* Docker nas apps + *file provider* dinâmico para processos nativos.
    Pulso escreve `dynamic/*.yaml` num volume compartilhado.
  - **Opção 2:** proxy nativo em Go (`net/http/httputil.ReverseProxy` +
    `golang.org/x/crypto/acme/autocert`). Menos dependências, mais código a manter.
- Tabela `domains` (id, app_id, host, path, port, https, cert_type, created_at).
- Rotas: `GET/POST/PUT/DELETE /api/v1/domains`, `POST /api/v1/domains/{id}/verify`.
- UI: aba **Domínios** na página do projeto, com estado do certificado.
- Validação de hostname, bloqueio de domínio duplicado, checagem de DNS antes de
  pedir o certificado (evita rate limit da Let's Encrypt).
- Teste: servidor ACME falso (`pebble`) no CI.

**Esforço:** G (Traefik) / GG (nativo) · **Dependências:** nenhuma

---

#### [x] A2. Deploy a partir de repositório Git
**O que é:** criar uma aplicação apontando para um repositório (URL pública,
chave SSH, ou provedor conectado por OAuth), escolher branch, build automático.

**Por que importa:** hoje o deploy exige que o código **já esteja** no servidor
(`ApplicationSource.Path`). O fluxo real é `git push` → deploy.

**Escopo técnico sugerido:**
- `internal/gitsource`: clone raso (`--depth 1 --single-branch`), via
  `go-git/go-git/v5` (puro Go, sem dependência do binário git) ou exec do `git`.
- Tabela `git_credentials` (nome, tipo, token/chave privada cifrada com
  `internal/secrets`) e campos `repo_url`, `branch`, `build_path` em `applications`.
- Fases no `containerupdate`: `cloning` → `building` → `starting` → `swapping`.
- Guardar o SHA do commit implantado para exibir e comparar.
- Começar só com **URL + branch + chave SSH/token**; OAuth dos provedores é A2b.

**Esforço:** M · **Dependências:** nenhuma (combina muito bem com A3 e A4)

---

#### [x] A2b. Provedores Git conectados (GitHub / GitLab / Gitea / Bitbucket)
**O que é:** OAuth App ou GitHub App, lista de repositórios e branches no
dropdown, status de deploy publicado de volta no commit.

**Escopo:** `internal/gitprovider` com uma interface por provedor; callback
OAuth em `/api/auth/providers/{provider}/callback`; tabela `git_providers`.
Dokploy implementa 4 provedores — comece por **um** (GitHub).

**Esforço:** M por provedor · **Dependências:** A2

---

#### [x] A3. Webhook de deploy (push-to-deploy)
**O que é:** cada app recebe uma URL secreta; um `POST` nela dispara o deploy.
GitHub/GitLab apontam o webhook para lá; qualquer CI também consegue chamar.

**Por que importa:** é o item de maior valor por linha de código do documento.
Transforma o Pulso em destino de CI/CD com pouquíssimo esforço.

**Escopo técnico sugerido:**
- Coluna `deploy_token` (aleatório de 32 bytes) em `applications`.
- `POST /api/deploy/{token}` — **fora** do middleware de sessão, com rate limit.
- Validação da assinatura HMAC do GitHub (`X-Hub-Signature-256`) e do GitLab
  (`X-Gitlab-Token`), em comparação de tempo constante.
- Filtro por branch e, opcionalmente, por caminho alterado (monorepo).
- Resposta `202` imediata; deploy em fila (um por app).
- UI: botão "copiar URL de deploy" e "regerar token".

**Esforço:** P · **Dependências:** A2 para builds de Git (sem A2, já serve para
re-pull de imagem)

---

#### [x] A4. Histórico de deploys e rollback por versão
**O que é:** cada deploy fica registrado com commit/tag, log de build completo,
duração, autor e resultado. Um clique volta para qualquer versão anterior.

**Por que importa:** o Pulso já faz rollback **automático** quando o candidato
falha — mas não há rollback **manual** para uma versão de ontem, nem histórico.

**Escopo técnico sugerido:**
- Tabelas `deployments` (app_id, status, commit, image_tag, build_log, started_at,
  finished_at, triggered_by) e `rollbacks` (snapshot do spec completo em JSON).
- Reter as N últimas imagens por app (configurável, default 3) e etiquetá-las
  `pulso/<app>:<deployment_id>` para que o rollback não precise rebuildar.
- Rotas: `GET /api/v1/applications/{id}/deployments`,
  `GET …/deployments/{dep}/logs` (stream), `POST …/deployments/{dep}/rollback`.
- UI: timeline por app; log de build com streaming via o WebSocket existente.
- GC: remover imagens fora da janela de retenção (liga com G4).

**Esforço:** M · **Dependências:** G1 (migrações) recomendada antes

---

### Grupo B — Dados e durabilidade

#### [ ] B1. Bancos de dados gerenciados
**O que é:** criar Postgres, MySQL/MariaDB, MongoDB, Redis (e Valkey) pelo
painel, com usuário/senha gerados, volume persistente, porta interna e
*connection string* pronta para colar nas variáveis da aplicação.

**Por que importa:** quase toda app precisa de um banco. Sem isto, o usuário
monta o banco à mão fora do Pulso e perde o sentido do painel.

**Escopo técnico sugerido:**
- `internal/services` com uma interface `ServiceRecipe` (imagem, env obrigatórias,
  porta, caminho de dados, comando de health, comando de dump/restore).
- Reaproveitar **todo** o `containerupdate` — um banco é só uma app com receita fixa.
- Tabela `managed_services`; senha cifrada em repouso.
- Injeção de referência: variável `DATABASE_URL=${service:meu-postgres}` resolvida
  pelo `internal/secrets` no momento do start. Evita copiar senha na mão.
- Rede Docker interna dedicada por projeto, para o banco **não** publicar porta no host.
- Começar por **Postgres + Redis** (cobre ~80% dos casos) e crescer por receita.

**Esforço:** M (2 engines) / G (7 engines) · **Dependências:** nenhuma

---

#### [ ] B2. Backups agendados com destino S3
**O que é:** cron de `pg_dump`/`mysqldump`/`mongodump`/`redis-cli --rdb` com
upload para S3 (ou qualquer endpoint S3-compatible: MinIO, R2, Backblaze),
política de retenção e restore pelo painel.

**Escopo técnico sugerido:**
- `internal/backup`: dump para stream → compressão gzip → upload multipart.
- Tabelas `backup_destinations` (credenciais cifradas) e `backup_schedules`.
- Reutilizar o `internal/scheduler` existente — ele já roda cron.
- Restore com confirmação dupla e dump de segurança **antes** de sobrescrever.
- Redigir segredos dos logs de backup (o Dokploy tem `backups/redact.ts` por isso).
- Teste: MinIO em contêiner no CI.

**Esforço:** M · **Dependências:** B1

---

#### [ ] B3. Backup e restore de volumes
**O que é:** tar do volume Docker de qualquer app (não só bancos) para o mesmo
destino S3, com a app parada ou em modo consistente.

**Escopo:** `internal/backup/volume.go`; contêiner auxiliar `alpine` com o volume
montado em modo leitura fazendo `tar -czf -`. Agenda reaproveitada de B2.

**Esforço:** P · **Dependências:** B2

---

### Grupo C — Formas de construir a aplicação

#### [x] C1. Suporte a `docker-compose.yml` do projeto
**O que é:** apontar para uma pasta com `compose.yaml` e o Pulso sobe o conjunto
de serviços como uma unidade, com logs e status agregados.

**Por que importa:** `docs/projects.md` declara explicitamente que "o recurso não
interpreta arquivos Compose de projetos como conjuntos de serviços". A maioria
dos projetos reais **é** um Compose. É a lacuna mais pedida depois de A1.

**Escopo técnico sugerido:**
- `internal/compose`: parse com `compose-spec/compose-go/v2` (biblioteca oficial).
- Executar via `docker compose -p pulso-<app> up -d` (mais simples e fiel) ou
  traduzir para a API do Engine (mais controle, muito mais código). **Recomendo o CLI.**
- Injetar labels `pulso.app=<id>` em todos os serviços, para listar/parar/remover.
- Sobrescrita gerada pelo Pulso (`pulso.override.yaml`) para domínios, limites e
  rede — sem tocar no arquivo do usuário.
- Bloquear diretivas perigosas (`privileged`, bind em `/`, `network_mode: host`)
  conforme a postura de segurança que o projeto já adota.

**Esforço:** M · **Dependências:** A1 para expor domínios dos serviços

---

#### [ ] C2. Catálogo de templates one-click
**O que é:** lista curada (n8n, Plausible, Umami, Uptime Kuma, Supabase,
Metabase, Grafana, WordPress…) que já vem com env, volumes, portas e domínio.

**Escopo:** templates em YAML embutidos no binário (`embed.FS`) + um índice
remoto opcional. Cada template é só um `ApplicationConfig` pré-preenchido com
placeholders e geradores (`{{randomPassword 32}}`, `{{domain}}`). Zero motor novo
se C1 ou B1 existirem.

**Esforço:** P pelo motor + 1–2 h por template · **Dependências:** C1 ou B1

---

#### [x] C3. Build packs sem Dockerfile (Nixpacks / Railpack)
**O que é:** detectar a linguagem e gerar a imagem sem o usuário escrever
Dockerfile, cobrindo Rust, Java, .NET, Elixir, Go etc.

**Por que importa:** hoje as receitas nativas cobrem Node e Python; o resto exige
Dockerfile. Nixpacks fecha isso com uma chamada de binário.

**Escopo:** `internal/containerupdate/buildpack.go` — baixar o binário `nixpacks`
(ou `railpack`) na imagem do Pulso e chamar `nixpacks build <dir> --name <tag>`.
Nova opção "Detecção automática" no formulário. ~200 LOC.

**Esforço:** P · **Dependências:** nenhuma

---

### Grupo D — Escala

#### [ ] D1. Multi-servidor via SSH
**O que é:** registrar servidores remotos e escolher em qual implantar cada app;
um painel central controlando N máquinas.

**Por que importa:** é o modelo comercial do Dokploy e o teto natural de
crescimento do Pulso. Também é a opção mais invasiva: toda operação de Docker e
de filesystem passa a precisar de um alvo.

**Escopo técnico sugerido:**
- `internal/remote`: interface `Executor` com implementações `local` e `ssh`
  (`golang.org/x/crypto/ssh`). **Refatorar `execAsync`/`docker.go` para passar
  pelo Executor é o pré-requisito** — fazer isso *antes* de crescer o resto.
- Tabela `servers` (host, porta, usuário, chave cifrada, estado, versão do Docker).
- Script de provisionamento idempotente (instalar Docker, abrir portas, criar rede).
- Health check periódico + disco/CPU/RAM do servidor no painel.

**Esforço:** G · **Dependências:** idealmente depois de A1/A2 estabilizados

---

#### [ ] D2. Réplicas e Docker Swarm
**O que é:** N instâncias da mesma app com balanceamento, rolling update e
`placement constraints`.

**Escopo:** o Dokploy usa Swarm (`CreateServiceOptions` do dockerode). No Go,
`docker/docker/client` já expõe `ServiceCreate`/`ServiceUpdate`. Exige repensar o
ciclo de deploy (hoje ele é "um contêiner candidato").

**Esforço:** G · **Dependências:** D1 para valer a pena

---

### Grupo E — Operação do dia a dia

#### [x] E1. Notificações
**O que é:** avisar em Slack, Discord, Telegram, e-mail (SMTP), Gotify, ntfy ou
webhook genérico quando: deploy sucede/falha, app reinicia em loop, healthcheck
cai, backup falha, disco/CPU passa de um limiar.

**Por que importa:** sem notificação, o painel só serve quando alguém está
olhando. Altíssima relação valor/esforço.

**Escopo técnico sugerido:**
- `internal/notify`: interface `Channel{Send(ctx, Event) error}`; um arquivo por
  canal (cada um é um `POST` JSON — ~40 LOC), SMTP via `net/smtp`.
- Tabela `notification_channels` (tipo, config JSON com segredos cifrados,
  eventos assinados) e `notification_log` (últimos envios, para depurar).
- Barramento: reaproveitar `internal/events` que **já existe** (`pulso events`).
  Um assinador traduz evento interno → mensagem.
- Templates de mensagem por evento, com link para o painel; botão "testar canal".
- Retry com backoff, timeout de 10 s, e nunca bloquear o deploy.

**Esforço:** M (4–5 canais) · **Dependências:** nenhuma — **recomendo como primeiro passo**

---

#### [ ] E2. Métricas históricas e gráficos no painel
**O que é:** séries temporais de CPU, memória, rede e disco por app e do servidor,
com gráficos de 1 h / 24 h / 7 d e limiares de alerta.

**Por que importa:** `internal/metrics` coleta mas **não guarda** — o painel só
mostra o instante atual, sem gráfico nenhum no frontend.

**Escopo técnico sugerido:**
- Armazenamento em SQLite com *downsampling* em camadas (10 s por 1 h → 1 min por
  24 h → 5 min por 30 d), limpeza por cron. Evita dependência de TSDB.
- Alternativa: expor só Prometheus (já existe `WritePrometheus`) e documentar
  Grafana. Muito mais barato, menos integrado.
- `GET /api/v1/applications/{id}/metrics?range=24h&step=1m`.
- Gráficos: SVG próprio ou `uPlot` (~40 KB, sem build step — combina com o
  frontend atual, que é HTML/CSS/JS puro sem bundler).
- Coletar métricas de **contêiner** também (`/containers/{id}/stats`), que hoje
  não são coletadas — só as de processo.

**Esforço:** M · **Dependências:** nenhuma

---

#### [ ] E3. Terminal no navegador (exec no contêiner e shell do servidor)
**O que é:** abrir um shell interativo dentro de um contêiner, ou no host, direto
do painel.

**Escopo:** `POST /api/v1/applications/{id}/exec` fazendo upgrade para WebSocket e
ligando em `ContainerExecAttach` do Engine; `xterm.js` no frontend. **Só admin**,
com auditoria obrigatória (E4), timeout de inatividade e opção de desligar por
env var. Risco de segurança alto — tratar como tal.

**Esforço:** P–M · **Dependências:** E4 recomendada junto

---

#### [ ] E4. Log de auditoria
**O que é:** registro imutável de quem fez o quê: login, deploy, mudança de env,
exclusão de app, criação de usuário, exec no terminal.

**Escopo:** tabela `audit_log` (actor, action, target, metadata JSON, ip,
user_agent, created_at) + middleware chi que grava nas rotas mutáveis. Página de
visualização com filtro. Retenção configurável. Redigir valores de env e senhas.

**Esforço:** P · **Dependências:** nenhuma

---

### Grupo F — Integração e acesso

#### [ ] F1. Tokens de API + especificação OpenAPI
**O que é:** tokens de longa duração com escopo, autenticando a mesma API REST
que o painel usa, e um `openapi.json` gerado — permitindo Terraform, scripts e
agentes externos.

**Por que importa:** o Pulso já tem uma API REST limpa em `/api/v1` e um SDK Go,
mas ela só aceita **cookie de sessão**. Um header `Authorization: Bearer` abre
todo o ecossistema de automação. O Dokploy mantém `openapi.json` versionado e
sincronizado com os docs por CI.

**Escopo técnico sugerido:**
- Tabela `api_tokens` (hash SHA-256 do token, nome, escopos, last_used_at,
  expires_at). Nunca guardar o token em claro; mostrar uma única vez.
- Middleware que aceita sessão **ou** bearer; escopos `read`, `write`, `admin`.
- OpenAPI: anotar os handlers e gerar com `swaggo/swag`, ou escrever o spec à mão
  e validar no CI contra as rotas do chi (teste que falha se uma rota não estiver
  documentada). **Recomendo a segunda** — spec menor e mais honesto.
- Rate limit por token.

**Esforço:** P–M · **Dependências:** E4 para auditar uso de token

---

#### [ ] F2. 2FA (TOTP) e SSO
**O que é:** segundo fator por app autenticador; opcionalmente OIDC/SAML.

**Escopo:** TOTP com `pquerna/otp` — ~150 LOC, coluna `totp_secret` cifrada +
códigos de recuperação. SSO/OIDC é bem maior (`coreos/go-oidc`) e só faz sentido
em uso corporativo. **Sugiro só o TOTP.**

**Esforço:** P (TOTP) / G (SSO) · **Dependências:** nenhuma

---

#### [ ] F3. Projetos, organizações e permissões granulares
**O que é:** agrupar apps em projetos/ambientes, convidar usuários e dar acesso
por projeto em vez dos dois papéis globais atuais.

**Escopo:** tabelas `projects`, `environments`, `memberships`;
`internal/auth/access.go` com checagem `can(user, action, resource)`.
É uma refatoração transversal — toda rota passa a precisar do escopo. Fazer
**antes** de D1, se D1 estiver no plano; depois fica muito mais caro.

**Esforço:** M–G · **Dependências:** nenhuma, mas ordene antes de D1

---

### Grupo G — Qualidade estrutural (sem feature nova)

Estes não aparecem na tela, mas são o que separa "projeto bom" de "projeto
confiável". Alguns deveriam vir **antes** das features acima.

#### [ ] G1. Migrações de schema versionadas do SQLite
**Por que:** o Dokploy tem 204 migrações versionadas; o Pulso cria tabelas
ad-hoc no `internal/auth`. Cada opção deste documento adiciona tabelas — sem um
motor de migração, atualizar instalações existentes vira trabalho manual e
arriscado. **Este é o pré-requisito técnico número um.**

**Escopo:** `internal/migrate/schema/NNN_nome.sql` com `embed.FS`, tabela
`schema_migrations`, aplicação em transação no boot, teste que sobe do zero e de
cada versão intermediária. `internal/migrate` já existe (1.502 LOC) para migrar
*dados* entre versões — estender a abordagem para *schema*.

**Esforço:** P · **Faça primeiro se escolher qualquer opção com tabela nova**

---

#### [ ] G2. i18n — separar texto de código
**Por que:** o código Go tem mensagens PT-BR fixas nos handlers
(`"informe uma imagem Docker válida"`, `"Entre como administrador…"`,
`"página indisponível"`), enquanto README e `docs/` estão em inglês e o frontend
mistura os dois ("Nova aplicação", "Editar"). Para um projeto open source isso
limita adoção e é visível como inconsistência.

**Escopo:** `internal/i18n` com catálogos `embed.FS` (`pt-BR.json`, `en.json`),
chaves estáveis (`error.image.invalid`), negociação por `Accept-Language` +
preferência do usuário, e `golang.org/x/text/message` se houver plural.
Um teste que falha quando uma chave existe num catálogo e falta no outro.
Decida também a língua canônica do projeto — hoje não há uma.

**Esforço:** M (mecânico, mas espalhado) · **Dependências:** nenhuma

---

#### [ ] G3. Preview deployments por pull request
**O que é:** cada PR aberto ganha um ambiente próprio em
`pr-42.app.seudominio.com`, destruído quando o PR fecha.

**Escopo:** clonar o `ApplicationConfig` com sufixo, domínio curinga
(`*.preview.dominio`) com certificado wildcard, TTL e limite de ambientes
simultâneos. Só vale com A1 + A2b + A3 prontos.

**Esforço:** M · **Dependências:** A1, A2b, A3

---

#### [ ] G4. Limpeza de disco e GC de imagens
**O que é:** painel mostrando uso de disco por app/volume/imagem, `docker system
prune` controlado, agendável, e retenção automática de imagens antigas.

**Por que:** servidores de deploy morrem de disco cheio. Com A4 (histórico de
imagens) isso deixa de ser opcional.

**Escopo:** `GET /api/v1/system/disk` (`df`, `/system/df` do Engine),
`POST /api/v1/system/prune` com *dry-run* obrigatório antes, e job no scheduler.

**Esforço:** P · **Dependências:** combina com A4

---

#### [ ] G5. Provedores de DNS e gestão de certificados
**O que é:** criar o registro DNS automaticamente (Cloudflare, Route53, OVH,
Porkbun) ao adicionar um domínio, e usar desafio DNS-01 para certificados
wildcard.

**Escopo:** `internal/dns` com uma interface por provedor; libera o wildcard que
G3 precisa e evita o desafio HTTP-01 atrás de CDN. Comece por Cloudflare.

**Esforço:** P por provedor · **Dependências:** A1

---

#### [ ] G6. Export e import de projeto
**O que é:** exportar a definição completa de um projeto (apps, env redigidas,
domínios, volumes declarados) em YAML e reimportar em outra instalação.

**Por que importa:** além de migração, é a base de *infrastructure as code* —
casa muito bem com o `pulso.yaml` que já existe para processos. Estender o mesmo
arquivo para cobrir contêineres é coerente com a arquitetura atual.

**Escopo:** `internal/projects/export.go`; segredos exportados como referências
(`${secret:nome}`), nunca em claro; `pulso apply -f projeto.yaml` no CLI.

**Esforço:** P–M · **Dependências:** nenhuma (ganha valor com B1 e A1)

---

#### [ ] G7. Reforço de testes e CI
**O que é:** fechar as lacunas pontuais de um CI que já é sólido.

**O que já existe** (verificado em `.github/workflows/ci.yml`): `golangci-lint`
v9.3.0 com action fixada por SHA, `govulncheck` v1.9.0 com `-test ./...`, matriz
de cross-compile, build multi-plataforma amd64+arm64 no `container.yml`,
Dependabot. Isso é melhor que a média do ecossistema — não precisa mexer.

**O que falta:**
- **Cobertura medida e publicada.** 77 arquivos de teste para 246 de código é
  bom, mas não há `-coverprofile` nem gate. Adicionar medição e um piso que
  só sobe.
- **Testes rodando em macOS e Windows.** A matriz hoje faz *cross-compile* (todos
  em `ubuntu-latest`), então o código `_unix`/`_windows` compila mas nunca é
  **executado** nessas plataformas. Adicionar `runs-on` real para os testes.
- **Scan da imagem** (Trivy ou Grype) no `container.yml` — hoje há build
  multi-plataforma, mas nenhuma varredura de CVE da imagem publicada.
- **Integração com Docker de verdade** via `testcontainers-go`:
  `internal/containerupdate/integration_test.go` existe mas depende do ambiente
  local; no CI ele provavelmente é saltado.
- **Smoke de upgrade:** instalar a versão N-1, atualizar para N, validar que os
  dados sobreviveram. O Dokploy tem `upgrade-integration-test.yml` só para isso,
  e cada opção deste documento que adiciona tabela aumenta esse risco.
- **SBOM + assinatura** das releases (`cosign`, `syft`) — o GoReleaser já está
  configurado e suporta ambos com poucas linhas.

**Esforço:** P–M · **Dependências:** nenhuma

---

#### [ ] G8. Documentação em inglês + site
**Por que:** 20 guias é muito acima da média, mas `docs/projects.md` e as
mensagens do produto estão em PT-BR e o README em inglês. Para um projeto público,
padronizar (e publicar com um gerador estático) multiplica a descoberta.

**Escopo:** padronizar a língua (ver G2), `docs/` em inglês com tradução PT-BR,
publicar via MkDocs Material ou Docusaurus no GitHub Pages, changelog gerado do
GoReleaser, e um `CONTRIBUTING.md` (o Dokploy tem; o Pulso não).

**Esforço:** P–M · **Dependências:** G2 para alinhar com o produto

---

## 4. Três caminhos recomendados

Não escolha item por item sem olhar a ordem — algumas opções ficam **muito** mais
caras se feitas fora de sequência.

### Caminho 1 — "Pronto para produção" (recomendado)
O mínimo para alguém hospedar um app real e dormir tranquilo.

```
G1 (migrações)  →  E1 (notificações)  →  A1 (proxy + TLS)  →  A2 (Git)
→  A3 (webhook)  →  A4 (histórico + rollback)  →  E4 (auditoria)  →  G4 (disco)
```
**Esforço:** ~6–8 semanas · **Resultado:** `git push` → build → domínio com HTTPS
→ aviso no Slack → rollback em um clique. É aqui que o Pulso passa a competir.

### Caminho 2 — "Plataforma completa"
Caminho 1 + os dados e as formas de build.

```
Caminho 1  →  C1 (Compose)  →  B1 (Postgres + Redis)  →  B2 (backups S3)
→  E2 (métricas históricas)  →  C3 (Nixpacks)  →  F1 (tokens + OpenAPI)  →  C2 (templates)
```
**Esforço:** +8–10 semanas · **Resultado:** paridade funcional com o Dokploy no
que importa, com a vantagem de CLI+TUI+MCP que ele não tem.

### Caminho 3 — "Esforço mínimo, ganho máximo"
Se o tempo é curto, só estes quatro:

```
E1 (notificações)  +  A3 (webhook de deploy)  +  F1 (tokens de API)  +  E4 (auditoria)
```
**Esforço:** ~2 semanas · **Resultado:** o Pulso vira integrável e observável
sem mudar sua arquitetura. Nenhum destes exige refatoração.

---

## 5. Diferenciais que o Pulso tem e o Dokploy não — vale proteger

Ao crescer, não perca o que já é vantagem competitiva:

1. **Binário único em Go** — o Dokploy exige Node + Postgres + Redis + Traefik.
   O Pulso roda com um arquivo. **Não introduza Postgres ou Redis como
   dependência obrigatória** em nenhuma opção acima; SQLite + goroutines dão conta.
2. **CLI e TUI de primeira classe** — toda feature nova deveria ganhar comando de
   CLI junto com a rota HTTP. O Dokploy é web-only.
3. **MCP nativo** — agentes de IA operando o deploy. Conforme A2/A4 entrarem,
   exponha-os como ferramentas MCP (`deploy_app`, `rollback_app`, `get_build_log`).
4. **Processos nativos + contêineres no mesmo painel** — ninguém mais faz isso.
   O proxy de A1 deve atender **os dois**, não só contêineres.
5. **Empacotamento Nix / NixOS / Home Manager** — mantenha os módulos atualizados
   a cada feature com configuração nova.

---

## 6. Como responder

Marque com `[x]` as opções escolhidas (edite este arquivo ou só me diga os
códigos, ex.: *"quero G1, E1, A1 e A3"* ou *"caminho 1"*).

Para cada item escolhido eu entrego, antes de escrever código:
- lista exata de arquivos novos e alterados;
- SQL das migrações;
- contratos das rotas (request/response, códigos de erro);
- plano de testes (unitário, integração, e2e);
- impacto em `docs/`, CLI, TUI, MCP, Compose e módulo Nix;
- riscos, ordem de implementação e pontos de decisão que dependem de você.
