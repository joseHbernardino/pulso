# Aplicações e arquivo .env no painel

Abra o painel no endereço configurado no servidor e entre como administrador.

1. Clique em **Nova aplicação** e informe um nome único.
2. Escolha o tipo de execução. **Automático pelo arquivo** reconhece Python,
   JavaScript, TypeScript, Go, Ruby e PHP. Para comandos como `npm` ou um binário,
   escolha **Executável ou comando**. Para uma linha de comando com canos,
   `&&` ou redirecionamento, para um arquivo `.sh` do servidor ou para um
   script escrito aqui, escolha **Script bash / comando shell** — aparece o
   campo **Script shell**, e o **Interpretador** passa a escolher o shell.
3. Use **Selecionar** para localizar o executável ou script e a pasta de trabalho.
   Os botões mostram os arquivos disponíveis para esta instalação. Ambos são caminhos no
   servidor em que o Pulso está rodando. Separe os argumentos do nome do programa.
4. Se a aplicação usa `.env`, informe o **Caminho do arquivo .env** e clique em
   **Carregar arquivo**. Caso contrário, deixe o campo vazio.
   Caminhos relativos usam a pasta de trabalho. Por exemplo, `.env` com a pasta
   `/srv/minha-api` aponta para `/srv/minha-api/.env`.
5. Edite o conteúdo. Se o arquivo não existir, ele será criado quando você salvar;
   a pasta precisa existir. Um arquivo vazio também é válido.
6. Em **Argumentos, ambiente e reinício**, informe os argumentos, um por linha,
   e ajuste o interpretador e a política de reinício, se necessário.
7. Clique em **Iniciar aplicação**.

Para testar, use **Preencher exemplo** e clique em **Iniciar aplicação**. O exemplo
executa `sleep 3600` sem `.env`; remova-o com **Excluir** ao terminar.

## Editar uma aplicação existente

Clique no botão **Editar**, com o ícone de lápis, na linha da aplicação. A tela
abre a configuração salva e o arquivo `.env`. Você pode alterar o programa,
argumentos, tipo de execução, pasta, interpretador, política de reinício,
variáveis explícitas e caminho ou conteúdo do `.env`. O nome permanece fixo.

Clique em **Salvar alterações**. O arquivo é gravado no servidor e a configuração
é salva para recuperação pelo daemon; o processo atual continua em execução.
O aviso **Reinício pendente** identifica as aplicações que ainda precisam aplicar
as alterações. Depois, clique em **Reiniciar aplicação** nessa tela ou em
**Reiniciar** na lista. Alterações não salvas precisam ser salvas antes de usar o
botão de reinício do editor.

O `.env` é relido a cada inicialização do processo, inclusive quando ele volta
após uma falha ou reinicialização da máquina. Alterações nas opções de execução
ficam aguardando um reinício manual ou a próxima recuperação do daemon.
Aplicações que leem o arquivo por conta própria podem perceber a gravação antes
do reinício. Um observador de arquivos configurado fora do painel também pode
reiniciar a aplicação ao detectar a edição.

Se outro editor modificar o arquivo depois de carregado, o Pulso recusa a gravação
para evitar sobrescrever aquela versão. Copie suas alterações, clique em
**Recarregar arquivo**, revise e salve novamente. Se a configuração mudar em outra
sessão, feche e abra a edição para obter a versão atual.

## Como o ambiente é carregado

A prioridade, da menor para a maior, é:

1. Ambiente herdado pelo daemon.
2. Variáveis do arquivo `.env`.
3. Variáveis explícitas do campo **Variáveis de ambiente** ou da opção `env`.

O Pulso preserva o texto do arquivo, incluindo comentários, aspas e quebras de
linha. Aceita comentários `#`, `export NOME=valor`, valores entre aspas e
interpolação de variáveis conforme o parser dotenv. Não executa comandos de
shell contidos no arquivo. Para manter um valor com `$` literal, use aspas simples.
O limite é de 64 KiB em UTF-8.

Cada aplicação recebe seu próprio ambiente; carregar um `.env` não altera o
ambiente global do daemon. Se várias aplicações usam o mesmo arquivo, a edição
marca todas como pendentes. Reinicie cada uma para carregar os novos valores.

## Acesso e arquivos

A leitura e a edição do `.env` são exclusivas de administradores. Usuários com
perfil de visualização não recebem seu caminho nem seu conteúdo. Os dados do
editor não são salvos no armazenamento do navegador.

O caminho deve apontar para um arquivo regular com nome `.env`, `.env.producao`
ou `aplicacao.env`. Links simbólicos não são aceitos. Novos arquivos usam
permissão `0600`; arquivos existentes preservam permissões, proprietário e grupo.
O usuário Linux que executa o Pulso precisa ter acesso à pasta e ao arquivo.

O caminho do `.env` é opcional. Aplicações que não usam esse arquivo podem ser
criadas e editadas com o campo vazio. Para deixar de carregar um `.env`, limpe o
caminho, salve e reinicie; o arquivo existente permanece no servidor. A
configuração por arquivo, CLI e clientes da API também aceitam processos sem `.env`.

O painel inicia programas já presentes no servidor; não faz upload do projeto.
As linguagens e dependências da aplicação precisam estar instaladas no servidor.
Use **Ver logs**, **Parar**, **Reiniciar** e **Excluir** para gerenciar o processo.
Para recuperação após reinicialização do Linux, configure o
[serviço de inicialização](boot-recovery.md).

Em Docker, volumes reconhecidos ou aliases configurados permitem informar o
caminho do host; o painel o traduz para o contêiner. Disponibilize uma pasta
raiz para atender todos os projetos abaixo dela. Veja
[reconhecimento de caminhos no Docker](docker.md#reconhecer-caminhos-de-vários-projetos).

## Console de comandos

**Console**, no menu lateral, executa um comando ou script neste servidor e
mostra a saída enquanto ela acontece. A página oferece pasta de trabalho,
contexto de aplicação (que empresta pasta, variáveis e `.env`), interpretador,
tempo limite, comandos prontos, histórico no próprio navegador e
**Parar comando**.

Fechar a página não interrompe o comando: uma migração não deve morrer no meio.
O que encerra é o tempo limite ou **Parar comando**.

Três regras valem sempre:

- **Só administradores.** Perfil de visualização e sessão ausente são
  recusados pelo servidor, não apenas pela interface. Uma instalação sem contas
  não tem administrador, então o console também recusa.
- **Tudo fica auditado.** Cada tentativa entra como `command.exec`, com a
  primeira linha do comando e a conta que executou, inclusive as recusadas.
  Trechos `NOME=valor` são gravados como `NOME=***`.
- **Há limites.** No máximo quatro comandos ao mesmo tempo, 30 minutos por
  comando e um teto de saída exibida — o comando continua, só a exibição para
  de crescer.

O console roda onde o painel roda. Em Docker, isso é dentro do contêiner do
painel. Para agir dentro do contêiner de uma aplicação, use as ações da própria
aplicação na página **Projetos**.

Detalhes, incluindo a API `/api/v1/exec`, em
[comandos e scripts shell](shell-commands.md).
