# Inicialização e recuperação automática no Linux

O Pulso salva alterações das aplicações e restaura o último estado confirmado
quando o daemon volta. Para iniciar mesmo sem login e reiniciar o gerenciador
após uma falha, instale-o como serviço do sistema operacional.

O instalador abaixo utiliza **systemd**, presente em Ubuntu, Debian, Red Hat
Enterprise Linux, Rocky Linux, AlmaLinux, CentOS Stream, Fedora e outras
distribuições. O executável não exige systemd para gerenciar processos; em
sistemas com OpenRC, runit ou outro init, integre o comando `pulso daemon run`
ao supervisor dessa distribuição. O instalador systemd não atende esses inits.

## Instalação como serviço

Use sempre o mesmo usuário e diretório de dados. O exemplo corresponde ao
ambiente atual, administrado por `root`, com dados em `/root/.pulso`. As contas
do painel continuam nesse diretório. Um usuário diferente tem uma instalação
independente, a menos que os dados sejam migrados explicitamente.

No diretório do projeto, depois de compilar a versão atual:

```bash
# Instale o executável em um caminho estável.
sudo install -m 0755 bin/pulso /usr/local/bin/pulso

# Se o daemon já está rodando manualmente, preserve a lista atual e pare-o
# antes de transferir o controle ao systemd. Execute como o usuário atual dele.
/usr/local/bin/pulso save
/usr/local/bin/pulso daemon stop

# Execute estas linhas na pasta em que você normalmente inicia suas aplicações.
sudo /usr/local/bin/pulso startup --system --web-listen 127.0.0.1:9615
sudo systemctl daemon-reload
sudo systemctl enable --now pulso.service
```

O comando `startup` instala a unidade e mostra os comandos de ativação; ele não
inicia outro daemon automaticamente. Para apenas revisar a unidade, use
`pulso startup --system --print`.

Se há um arquivo de configuração, passe o caminho explicitamente:

```bash
sudo /usr/local/bin/pulso --config /etc/pulso/pulso.yaml startup --system
```

A unidade mantém os caminhos de executável, configuração, diretório de trabalho
e PATH existentes na instalação. Mantenha esses caminhos disponíveis após o
boot. Os diretórios de trabalho das aplicações são gravados como caminhos
absolutos. Bibliotecas, interpretadores e bancos externos usados pelas
aplicações também precisam estar disponíveis. Variáveis que existiam apenas
no terminal devem ser definidas explicitamente na aplicação ou configuração; o
ambiente inteiro do terminal não é copiado para o serviço.

## Uso e acompanhamento

```bash
systemctl status pulso.service
journalctl -u pulso.service -f
pulso list
```

O painel inicia junto com o daemon e fica em `http://127.0.0.1:9615`. O endereço
pode ser escolhido com `startup --web-listen`. O login e as permissões existentes
continuam sendo aplicados.

```bash
sudo systemctl restart pulso.service  # reinicia e recupera os serviços
sudo systemctl stop pulso.service     # para o gerenciador e suas aplicações
sudo systemctl start pulso.service    # volta ao estado salvo
```

Para iniciar apenas manualmente, use `pulso web`: o comando inicia o daemon e o
painel. `Ctrl+C` encerra a espera do terminal; o daemon continua ativo.

## O que é recuperado

- Criar, parar, reiniciar, recarregar ou excluir uma aplicação atualiza o arquivo
  `dump.json` automaticamente no daemon. Não é preciso executar `save` a cada
  alteração. O comando continua disponível para salvar explicitamente uma lista.
- Aplicações ativas são iniciadas novamente; as deliberadamente paradas
  permanecem cadastradas e paradas; as excluídas não são recriadas.
- Nome, argumentos, ambiente, pasta de trabalho e política de reinício são
  preservados. Os processos recebem novos PIDs. Não há restauração da memória
  interna de um programa ou de uma requisição que estava em andamento.
- Encerrar o gerenciador não substitui o estado salvo por uma lista vazia.
- O arquivo é privado (`0600`), escrito em arquivo temporário, sincronizado com
  o disco e substituído atomicamente. Erros de persistência são informados.
- Ao restaurar, um arquivo corrompido ou uma aplicação que não pode ser iniciada
  causa erro visível no journal; o snapshot original não é apagado.
- A lista salva prevalece sobre entradas de configuração com o mesmo nome.
  Outras entradas com `autostart: true` são adicionadas depois da restauração.

A política `restart_policy` da aplicação trata a queda **do programa** enquanto o
Pulso está ativo. O systemd trata a queda **do gerenciador** e o boot da máquina.
Use `always` para um serviço que deve permanecer em execução ou `on-failure`
para reiniciar somente após erro. `never` desativa a repetição automática do
processo durante a sessão; uma aplicação que estava ativa ainda é recuperada
quando o gerenciador reinicia.

## Serviço sem privilégios de root

Execute como o usuário que administrará as aplicações:

```bash
pulso startup --system=false
systemctl --user daemon-reload
systemctl --user enable --now pulso.service
sudo loginctl enable-linger "$(id -un)"
```

O linger mantém o gerenciador de serviços do usuário disponível sem uma sessão
interativa. Não ative simultaneamente unidades de usuário e de sistema sobre o
mesmo diretório de dados.

## Limites da recuperação da máquina

Um serviço systemd inicia depois que o Linux já iniciou. Para voltar de uma
interrupção de energia, a máquina física ou hipervisor precisa estar configurado
para religar e iniciar o Linux. No WSL, habilitar um serviço Linux não configura
a inicialização automática da distribuição depois que o Windows reinicia.

O teste desta implementação usou uma unidade systemd isolada: queda forçada do
daemon com SIGKILL, recuperação da aplicação ativa, preservação da parada,
ausência da excluída, retorno do painel e reinício normal. A unidade gerada foi
verificada com `systemd-analyze verify`. Isso não equivale a desligar fisicamente
a máquina nem a executar testes em cada distribuição Linux.
