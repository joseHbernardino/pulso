# Identidade Pulso

Pulso acompanha o ritmo das aplicações. O nome tem cinco letras, duas sílabas e
funciona da mesma forma na marca, no terminal e no arquivo de configuração.

## Sistema visual

| Cor | Hex | Uso |
| --- | --- | --- |
| Petróleo | `#153F46` | Marca e fundo institucional |
| Turquesa | `#087D6C` | Interação, foco e elementos de destaque sobre fundo claro |
| Menta | `#42C5B6` | Símbolo e destaques sobre petróleo |
| Âmbar | `#F1B963` | Nó central do símbolo e atenção sobre fundo escuro |
| Nuvem | `#F5F7F8` | Fundo do espaço de trabalho |
| Tinta | `#233C40` | Texto principal |

O símbolo é um P aberto: uma haste e uma volta que representa o ciclo de
execução. O ponto âmbar representa o processo supervisionado. Não alterar as
proporções, inclinar o símbolo ou adicionar efeitos de brilho.

A interface e a marca usam Manrope, incluída no próprio binário sob a licença
SIL Open Font License. Código e identificadores usam Cascadia Code /
SFMono-Regular / Consolas. Não há requisições externas para carregar fontes.
Aplicações e Usuários compartilham o mesmo menu, cabeçalho, menu da conta,
componentes e estilos por meio de `shell.js`, `brand.css` e `dashboard.css`.
No celular, a navegação fica em um menu recolhível e as tabelas permitem rolagem
horizontal. Os filtros funcionam sobre os dados reais do servidor.

O painel prioriza uma tabela de aplicações, estados legíveis e ações diretas.
A cor reforça o estado, sempre acompanhado de texto. O login usa uma composição
em duas áreas no desktop e uma coluna no celular. O foco de teclado é visível
e animações respeitam a preferência por movimento reduzido.

## Arquivos

- [Logo principal](../assets/brand/pulso-logo.svg)
- [Logo sobre fundo escuro](../assets/brand/pulso-logo-on-dark.svg)
- [Símbolo e favicon](../assets/brand/pulso-mark.svg)
- [Prancha de identidade](../assets/brand/preview.html)

Os SVGs têm fundo transparente e podem ser escalados sem perda de definição.
Mantenha espaço livre de pelo menos um quarto da altura do símbolo ao redor da
marca. Para ícones pequenos, use apenas o símbolo.

## Identidade técnica

Comando: `pulso`. Configuração: `pulso.yaml`. Dados: `~/.pulso/`.
Variáveis: `PULSO_*`. Cookie de sessão: `pulso_session`.
Módulo local: `pulso.local/pulso`.

A nova identidade não importa automaticamente configurações ou dados de outras
instalações. Use arquivos explicitamente revisados. O aviso de copyright
original permanece integralmente no arquivo LICENSE, conforme a licença MIT.
