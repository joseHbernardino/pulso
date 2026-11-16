# Login, usuários e permissões

O painel iniciado pelo daemon exige login. Usuários e sessões ficam em
`~/.pulso/security/auth.sqlite`, sem um servidor de banco separado. O driver
SQLite funciona com `CGO_ENABLED=0`.

## Primeiro acesso

1. Inicie o painel com `./bin/pulso web --listen 127.0.0.1:9615`.
2. Abra `http://127.0.0.1:9615/login`.
3. Leia o código de configuração no servidor:

   ```bash
   cat ~/.pulso/security/setup-token
   ```

4. Informe o código, um nome de usuário e uma senha de pelo menos 12 caracteres.
   O limite da senha é de 72 bytes, devido ao bcrypt.
5. Confirme a senha e clique em **Criar administrador**.

Não existe senha padrão. O código permite criar apenas a primeira conta e é
removido depois da configuração. Esse arquivo e o banco têm permissão `0600`.
O diretório `security` é criado com permissão `0700`.

## Perfis

| Ação | Visualização (`view`) | Administrador (`admin`) |
| --- | --- | --- |
| Listar processos e consultar métricas | Sim | Sim |
| Consultar logs | Sim | Sim |
| Consultar variáveis e argumentos dos processos | Não | Sim |
| Criar, parar, reiniciar, recarregar e excluir aplicações | Não | Sim |
| Criar usuários, alterar perfis e redefinir senhas | Não | Sim |
| Ativar e desativar contas | Não | Sim |

As permissões são verificadas no servidor, incluindo chamadas diretas à API.
Esconder botões não é o controle de acesso. As rotas administrativas retornam
`403` para leitores; sessões ausentes ou revogadas recebem `401`.

## Gerenciar usuários

Entre como administrador e abra **Usuários** na navegação do painel.

- **Novo usuário** cria uma conta com o perfil escolhido. O padrão é Visualização.
- **Editar** altera o perfil e permite redefinir a senha. Deixe a senha vazia para
  manter a atual.
- **Desativar** bloqueia novos acessos e revoga as sessões existentes.
- **Ativar** permite que a pessoa entre novamente com sua senha.

Mudanças de perfil e de senha também encerram as sessões do usuário. O último
administrador ativo não pode ser excluído, desativado ou rebaixado. Não há
cadastro público nem recuperação de senha por e-mail nesta versão.

## Sessões e armazenamento

As senhas usam bcrypt. O SQLite guarda somente o hash dos tokens de sessão.
Cookies de sessão são HttpOnly e SameSite=Lax; conexões TLS diretas também usam
Secure. As sessões duram 24 horas e sobrevivem ao reinício do serviço.
O botão **Sair** revoga a sessão. WebSockets verificam a validade da sessão ao
transmitir atualizações e desconectam contas revogadas.

O banco pode ser copiado com a API de backup do SQLite ou com o serviço parado;
em modo WAL, copiar somente `auth.sqlite` enquanto o serviço escreve pode perder
alterações recentes. Proteja os backups com as mesmas permissões do banco.

Estes perfis controlam o painel e sua API HTTP. O terminal, o socket Unix e o MCP
continuam com sua própria configuração e com as permissões do sistema
operacional. `security.auth` configura essas interfaces; não desativa o login
persistente do painel iniciado pelo daemon. Aplicações incorporadas que usam o
construtor legado `NewServerWithAuth` mantêm seu comportamento anterior; novas
integrações podem usar `NewServerWithUsers` com um banco aberto explicitamente.

O painel permanece acessível apenas pelo endereço em que foi iniciado. Para
acesso remoto, mantenha-o atrás de HTTPS e não trate cabeçalhos de IP enviados
pelo visitante como prova de acesso local.
