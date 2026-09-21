# Arquitetura

Este documento descreve o formato do arquivo, o protocolo de durabilidade e as
regras que o planejador aplica. A ideia é que seja possível reconstruir o
raciocínio sem ler o código linha a linha.

## Camadas

```
            +-----------------------------------------+
  SQL  -->  |  internal/sql     léxico, sintaxe, AST   |
            +-----------------------------------------+
                            |
            +-----------------------------------------+
            |  internal/engine  catálogo, planejador,  |
            |                   operadores, execução   |
            +-----------------------------------------+
                            |
            +-----------------------------------------+
            |  internal/storage páginas, pool, B+,     |
            |                   log de escrita         |
            +-----------------------------------------+
                            |
                      arquivo de dados + log
```

`internal/types` fica ao lado de todas elas: define o valor SQL, as regras de
comparação e as duas codificações usadas no disco.

## Formato do arquivo

O arquivo é uma sequência de páginas de 4096 bytes. A página 0 guarda os
metadados; as demais são nós da árvore, páginas de transbordo ou entradas da
lista de páginas livres.

### Metadados (página 0)

| Deslocamento | Tamanho | Conteúdo |
| --- | --- | --- |
| 0 | 8 | assinatura `CORVODB\x01` |
| 8 | 4 | tamanho da página |
| 12 | 8 | total de páginas |
| 20 | 8 | primeira página livre |
| 28 | 8 | raiz da árvore do catálogo |
| 36 | 8 | último identificador de transação |
| 44 | 4 | checksum CRC32 dos 44 bytes anteriores |

### Nó da árvore B+

```
 0      tipo (folha ou interno)
 1      sinalizadores
 2..3   número de chaves
 4..5   início da área de células
 6..7   reservado
 8..15  próxima folha, ou filho mais à direita em nó interno
16..23  folha anterior
24..    vetor de posições, 2 bytes por célula
        ...espaço livre...
        células, crescendo do fim da página para o início
```

O vetor de posições fica ordenado por chave e as células ficam no fim da página.
Inserir no meio move apenas dois bytes por célula deslocada, não o conteúdo. O
espaço deixado por células removidas só volta a ser usado depois de uma
compactação, que acontece sob demanda quando o espaço contíguo não basta mas o
total ainda é suficiente.

Uma célula de folha tem duas formas:

```
sinalizador(1) tamanhoChave(2) tamanhoValor(4) chave valor          valor embutido
sinalizador(1) tamanhoChave(2) tamanhoTotal(4) páginaTransbordo(8) chave
```

Valores acima de 800 bytes vão para uma corrente de páginas de transbordo, cada
uma com o ponteiro para a próxima nos primeiros 8 bytes e o tamanho útil nos 4
seguintes. Chaves são limitadas a 512 bytes, o que garante pelo menos sete
separadores por nó interno e três células por folha.

Em um nó interno, o filho guardado na célula `i` contém todas as chaves menores
que a chave da célula `i`; o campo de ligação guarda o filho das chaves restantes.

## Durabilidade

O log fica em um arquivo separado, `<banco>-log`, e contém apenas registros de
refazer:

```
tipo(1) transação(8) página(8) tamanho(4) conteúdo checksum(4)
```

O protocolo é este:

1. Ao modificar uma página, a transação guarda uma cópia do conteúdo anterior em
   memória e marca a página como presa no pool de buffers.
2. Uma página presa nunca é despejada, então o arquivo de dados jamais recebe
   dado de transação não confirmada. Essa é a política de *no-steal*, e é o que
   torna desnecessário registrar como desfazer.
3. No commit, a imagem de cada página modificada vai para o log, seguida do
   registro de commit, e o log é sincronizado com `fsync`. A partir desse ponto a
   transação está durável, mesmo que as páginas ainda não tenham saído do pool.
   Essa é a política de *no-force*.
4. O checkpoint grava as páginas sujas no arquivo de dados, sincroniza e zera o
   log.
5. No rollback, as cópias guardadas no passo 1 voltam para o pool e os metadados
   são restaurados.

Como a recuperação grava no arquivo de dados, ela não pode rodar em modo somente
leitura. Abrir um banco com log pendente em modo somente leitura falha com uma
mensagem explícita, em vez de servir o estado anterior à falha.

A recuperação, executada na abertura, lê o log do começo ao fim, acumula os
registros de cada transação e aplica ao arquivo de dados apenas os de transações
que chegaram a gravar o commit. Um registro truncado ou com checksum inválido
encerra a leitura: é exatamente o que uma falha no meio de um commit deixa para
trás, e tudo daquela transação é descartado.

## Concorrência

Um mutex de leitura e escrita cobre a duração de cada transação. Escritas são
exclusivas entre si e em relação às leituras; leituras correm em paralelo. Um
segundo mutex, mais curto, protege o pool de buffers e o descritor do arquivo.

Como leitores e escritores nunca se sobrepõem, um leitor sempre enxerga o último
estado confirmado sem precisar de versionamento.

## Catálogo

O esquema mora em uma árvore B+ própria, cuja raiz fica nos metadados. Cada
tabela é uma entrada com chave `table:<nome>` e valor em JSON, o que mantém o
formato legível e fácil de estender.

Em memória, o catálogo é um instantâneo imutável. Uma transação de escrita
trabalha sobre uma cópia privada e só publica essa cópia no commit, então um
`CREATE TABLE` revertido não deixa rastro nem no disco nem na memória.

## Linhas e índices

A árvore da tabela usa o identificador da linha como chave e a linha codificada
como valor. Quando a tabela declara `id INTEGER PRIMARY KEY`, o valor dessa coluna
é o próprio identificador; caso contrário o identificador é um contador interno.

Um índice secundário é outra árvore B+, com chave `(valor, identificador)` e valor
vazio. Guardar o identificador na chave resolve dois problemas de uma vez: chaves
duplicadas continuam distintas, e uma busca por prefixo encontra todas as linhas de
um mesmo valor. A verificação de unicidade é uma busca por esse prefixo, e valores
nulos ficam de fora dela, como manda o padrão.

### Codificação ordenada

| Tipo | Codificação |
| --- | --- |
| NULL | `0x00` |
| BOOLEAN | `0x01` seguido de 0 ou 1 |
| INTEGER | `0x02` seguido de 8 bytes big-endian com o bit de sinal invertido |
| FLOAT | `0x03` seguido dos bits IEEE 754 ajustados para que negativos ordenem antes |
| TEXT | `0x04` seguido do texto com `0x00` escapado como `0x00 0xFF` e terminador `0x00 0x00` |

A inversão do bit de sinal faz com que inteiros negativos ordenem antes dos
positivos na comparação byte a byte. O escape do zero garante que a codificação de
`'a'` seguida de `'b'` nunca colida com a de `'ab'` seguida de outra coisa, o que
mantém as chaves compostas sem ambiguidade.

## Execução

O executor segue o modelo de iteradores: o plano é uma árvore de operadores e cada
chamada a `Next` puxa uma linha da raiz, que puxa das folhas. Nada é materializado
além do necessário, com duas exceções inevitáveis: a ordenação e o lado de
construção da junção por hash.

Operadores disponíveis: varredura sequencial, varredura por chave primária,
varredura por índice, filtro, junção por hash, junção por laço aninhado,
agregação por hash, projeção, ordenação, distinção e limite.

### Regras do planejador

1. O `WHERE` é quebrado em conjunções separadas por `AND`.
2. Cada conjunção que menciona uma tabela só desce até o ponto logo acima da
   varredura daquela tabela.
3. Conjunções da forma `coluna <operador> constante` viram limites de intervalo.
   Limites do mesmo campo são combinados, e o valor é convertido para o tipo
   declarado da coluna antes de virar chave, porque a codificação depende do tipo.
4. Entre os candidatos, ganha o de maior pontuação: igualdade vale mais que
   intervalo fechado, que vale mais que intervalo aberto. A chave primária ganha
   um ponto extra, porque dispensa o segundo acesso.
5. As conjunções consumidas pelo intervalo saem do filtro. A varredura verifica os
   limites de forma exata, então não há reavaliação redundante.
6. Um `ON` de igualdade que cruza os dois lados vira junção por hash, com o lado
   direito carregado na tabela de dispersão. O resto da condição vira resíduo
   avaliado depois do encontro.
7. Se não houver predicado útil, mas o `ORDER BY` for uma única coluna com índice,
   a varredura usa esse índice na direção pedida e a ordenação some do plano.

`EXPLAIN` imprime a árvore resultante, com um nível de indentação por profundidade.

### Agregação

Expressões que já foram calculadas pelo operador de agregação, como as chaves do
`GROUP BY` e as próprias chamadas de agregação, não são reavaliadas acima dele.
O planejador compara estruturalmente as expressões da lista de seleção com as do
agrupamento e registra um mapa de substituição, que os operadores superiores
consultam antes de avaliar qualquer nó.

Esse mesmo mapa serve de validação: uma coluna que sobra fora dele é uma coluna
que não está agrupada nem agregada, e a consulta é rejeitada em vez de devolver
uma linha arbitrária do grupo.
