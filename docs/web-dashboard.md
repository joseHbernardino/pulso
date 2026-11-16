# Iniciar aplicações pelo painel

Abra o painel em `http://127.0.0.1:9615` e clique em **Nova aplicação**.

1. Informe um nome único, como `minha-api`.
2. Escolha o tipo de execução. Automático reconhece arquivos Python, JavaScript,
   TypeScript, Go, Ruby e PHP pela extensão. Para comandos como `npm`, `python3`
   ou um binário próprio, escolha **Executável ou comando**.
3. Informe o executável ou script. O arquivo precisa existir no servidor em que
   o Pulso está rodando. Para um comando completo, separe o programa dos seus
   argumentos: `npm` no campo de executável; `run` e `start` em linhas separadas
   no campo Argumentos.
4. Informe a pasta de trabalho, se necessário. Caminhos relativos ao script são
   resolvidos nessa pasta. Sem uma pasta explícita, o processo usa a pasta de
   inicialização do Pulso.
5. Em **Argumentos, ambiente e reinício**, informe um argumento por linha e uma
   variável por linha no formato `NOME=valor`. Valores podem conter espaços ou
   outros sinais de igual. Não adicione aspas de shell aos argumentos.
6. Clique em **Iniciar aplicação**. A aplicação aparece imediatamente na lista.

Para experimentar sem um projeto próprio, clique em **Preencher exemplo** e
depois em **Iniciar aplicação**. O exemplo usa `sleep 3600`, sem reinício
automático. Remova-o depois com **Excluir**.

Use **Ver logs** para ver a saída, **Parar** para parar, **Reiniciar** para reiniciar e
**Excluir** para parar e remover o cadastro. O processo continua supervisionado
quando a página ou a requisição que o iniciou é encerrada.

O painel mantém os dados preenchidos quando há um erro, como nome duplicado,
variável inválida ou executável inexistente. Ao cancelar ou concluir o
cadastro, o formulário é limpo. As variáveis não são gravadas no armazenamento
do navegador.

Este formulário inicia programas já presentes no servidor. Não envia arquivos
do computador do visitante. As linguagens e dependências da aplicação precisam
estar instaladas no servidor. O daemon salva as alterações automaticamente para recuperação após reinício.
Para iniciar junto com o Linux, configure o [serviço de inicialização](boot-recovery.md).
