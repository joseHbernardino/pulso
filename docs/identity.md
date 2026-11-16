# Identidade Pulso

Pulso acompanha o ritmo das aplicações. O nome tem cinco letras, duas sílabas e
funciona da mesma forma na marca, no terminal e no arquivo de configuração.

## Sistema visual

| Cor | Hex | Uso |
| --- | --- | --- |
| Petróleo | `#123D43` | Navegação, fundo institucional, ações principais |
| Turquesa | `#147D73` | Interação, foco e elementos de destaque sobre fundo claro |
| Menta | `#42C5B6` | Símbolo e destaques sobre petróleo |
| Âmbar | `#F1B963` | Nó central do símbolo e atenção sobre fundo escuro |
| Nuvem | `#F2F6F5` | Fundo do espaço de trabalho |
| Tinta | `#163137` | Texto principal |

O símbolo é um P aberto: uma haste e uma volta que representa o ciclo de
execução. O ponto âmbar representa o processo supervisionado. Não alterar as
proporções, inclinar o símbolo ou adicionar efeitos de brilho.

A interface usa Aptos / Segoe UI Variable Text / Segoe UI, com fontes locais de
reserva. A marca usa Trebuchet MS. Código e identificadores usam Cascadia Code /
SFMono-Regular / Consolas. Não há downloads de fontes ou dependências externas
para renderizar a interface.

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
