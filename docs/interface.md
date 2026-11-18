# Interface do painel

O painel do Pulso adapta a organização visual do núcleo aberto do Dokploy:
barra lateral flutuante, conteúdo em painéis, navegação compacta e cartões de
projetos. A implementação usa HTML, CSS e JavaScript incorporados no binário
Go. Não exige compilar ou executar o frontend React do Dokploy.

## Navegação e aparência

- **Projetos** reúne as aplicações em contêineres, com busca por nome, imagem
  ou caminho e filtros de estado. Cada cartão mostra origem, limites, portas,
  estado e acesso aos logs. O menu de ações permite editar, iniciar, parar,
  reiniciar ou excluir conforme as permissões da conta.
- **Processos** mantém os programas supervisionados diretamente pelo Pulso,
  incluindo métricas, diagnóstico e registros.
- **Usuários e acesso** permanece disponível para administradores. A conta
  conectada e a ação de sair ficam no rodapé da navegação.

O botão ao lado da localização recolhe a lateral no computador e abre o menu
no celular. A preferência de recolhimento é salva neste navegador. No celular,
o menu fecha pelo fundo, pelo botão ou por Escape; o foco permanece dentro
dele enquanto está aberto.

O seletor de aparência no cabeçalho oferece **Escuro**, **Claro** e **Sistema**.
O padrão é escuro. A escolha vale para login, projetos, processos, usuários e
diálogos, e fica salva no navegador. A opção Sistema acompanha o tema do
dispositivo. O indicador de versão continua abrindo as atualizações do Pulso.

Os formulários de projetos separam aplicação, rede e recursos e ambiente.
Seu cabeçalho e botões permanecem visíveis enquanto os campos rolam. Os
controles continuam usando as mesmas APIs e regras de autorização do Pulso.

## Créditos

As referências de layout e os tokens neutros vêm de
`apps/dokploy/components/layouts/side.tsx` e
`apps/dokploy/styles/globals.css`, fora dos diretórios proprietários do
Dokploy. A adaptação mantém a identidade Pulso e seus próprios componentes;
os créditos e a licença Apache 2.0 estão em `licenses/dokploy.*`.
A fonte Inter é servida localmente, com sua licença SIL Open Font License
incluída nos assets. O painel não carrega fontes ou scripts de CDNs.
