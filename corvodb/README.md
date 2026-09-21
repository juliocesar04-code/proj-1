# CorvoDB

Banco de dados relacional embutido, escrito do zero em Go, sem nenhuma dependência
externa. O projeto vai do byte no disco até a linguagem: páginas de 4 KB, árvore
B+ em disco, log de escrita antecipada com recuperação de falha, um dialeto de SQL
com parser próprio, um planejador que escolhe índices e um executor no modelo de
iteradores.

Não é um clone de SQLite nem pretende ser. É uma implementação enxuta e completa
o suficiente para mostrar como as peças de um banco de dados se encaixam.

```
$ corvo exemplos/loja.db
corvo> SELECT u.nome, COUNT(*) AS pedidos, ROUND(SUM(p.total), 2) AS gasto
   ...> FROM usuarios u
   ...> JOIN pedidos p ON p.usuario_id = u.id
   ...> WHERE p.status = 'pago'
   ...> GROUP BY u.nome
   ...> ORDER BY gasto DESC;
 nome          | pedidos | gasto
---------------+---------+-------
 Elisa Prado   |       1 |   240
 Ana Ribeiro   |       2 | 170.4
 Bruno Tavares |       1 |  59.9
 Carla Menezes |       1 |    15
(4 rows)

corvo> EXPLAIN SELECT id, total FROM pedidos WHERE status = 'pendente';
 plan
------------------------------------------------------------------------
 Projection: id, total
   Index Scan on pedidos using idx_pedidos_status (status = 'pendente')
(2 rows)
```

## O que está implementado

**Armazenamento**

- Arquivo paginado em blocos de 4 KB com página de metadados, lista de páginas
  livres e reaproveitamento de espaço.
- Árvore B+ com chaves e valores de tamanho variável, divisão de nós, folhas
  ligadas nos dois sentidos e páginas de transbordo para valores grandes.
- Pool de buffers com política LRU e fixação de páginas em uso.
- Log de escrita antecipada com checksum por registro, recuperação automática na
  abertura e checkpoint periódico.
- Transações com um escritor e vários leitores simultâneos, commit durável e
  rollback que restaura o estado anterior.

**SQL**

- `CREATE TABLE`, `DROP TABLE`, `CREATE INDEX`, `CREATE UNIQUE INDEX`, `DROP INDEX`
- `INSERT`, `UPDATE`, `DELETE`, `SELECT`
- `WHERE`, `INNER JOIN`, `GROUP BY`, `HAVING`, `ORDER BY`, `LIMIT`, `OFFSET`, `DISTINCT`
- `BEGIN`, `COMMIT`, `ROLLBACK`
- `EXPLAIN` para ver o plano escolhido
- Agregações `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, com suporte a `DISTINCT`
- Funções escalares `LOWER`, `UPPER`, `LENGTH`, `TRIM`, `ABS`, `ROUND`, `SUBSTR`,
  `COALESCE`, `IFNULL`, `TYPEOF`
- Operadores `LIKE`, `IN`, `BETWEEN`, `IS NULL`, concatenação com `||`, aritmética
  e lógica de três valores para `NULL`
- Tipos `INTEGER`, `FLOAT`, `TEXT`, `BOOLEAN`, com restrições `PRIMARY KEY`,
  `NOT NULL` e `UNIQUE`

**Planejamento de consultas**

- Quebra do `WHERE` em conjunções e empurra cada uma para o ponto mais profundo
  onde ela pode ser avaliada.
- Escolha entre varredura sequencial, varredura por chave primária e varredura por
  índice secundário, com intervalos fechados ou abertos.
- Junção por hash quando a condição é de igualdade, laço aninhado nos outros casos.
- `ORDER BY` que coincide com a ordem de um índice dispensa a ordenação.

## Instalação

```sh
go install github.com/juliocesar04-code/proj-1/corvodb/cmd/corvo@latest
```

Ou, a partir do repositório clonado:

```sh
make build     # gera bin/corvo
make test      # testes com detector de corrida
make bench     # benchmarks
```

Requer Go 1.24 ou superior. Nenhuma outra dependência.

## Usando o shell

```sh
corvo loja.db                        # abre o banco e lê comandos da entrada padrão
corvo -c "SELECT 1" loja.db          # executa e sai
corvo -f exemplos/loja.sql loja.db   # executa um arquivo
```

Comandos do shell:

| Comando | O que faz |
| --- | --- |
| `.tables` | lista as tabelas |
| `.schema [tabela]` | mostra a definição de uma tabela ou de todas |
| `.indexes` | lista os índices |
| `.timer [on\|off]` | mostra o tempo de cada comando |
| `.help` | ajuda |
| `.exit` | sai |

Comandos SQL terminam em ponto e vírgula e podem ocupar várias linhas.

## Usando como biblioteca

```go
db, err := engine.Open("loja.db", engine.Options{})
if err != nil {
    return err
}
defer db.Close()

session := db.Session()
if _, err := session.Exec(`
    CREATE TABLE produtos (id INTEGER PRIMARY KEY, nome TEXT NOT NULL, preco FLOAT);
    INSERT INTO produtos (nome, preco) VALUES ('teclado', 199.90);
`); err != nil {
    return err
}

results, err := session.Exec("SELECT nome, preco FROM produtos WHERE preco < 500")
if err != nil {
    return err
}
for _, row := range results[0].Rows {
    fmt.Println(row[0].S, row[1].F)
}
```

## Como está organizado

```
cmd/corvo/            shell interativo
internal/storage/     páginas, pool de buffers, árvore B+, log de escrita antecipada
internal/types/       valores SQL, comparação e codificação ordenada de chaves
internal/sql/         analisador léxico, sintático e árvore sintática
internal/engine/      catálogo, planejador, operadores e execução de comandos
```

As dependências entre pacotes apontam em uma direção só: `engine` conhece
`sql`, `storage` e `types`; `storage` não conhece SQL; `types` não conhece nada.

O documento [docs/arquitetura.md](docs/arquitetura.md) detalha o formato do
arquivo, o protocolo de recuperação e as regras do planejador.

## Decisões de projeto

**Por que árvore B+ e não uma LSM tree.** O objetivo aqui é leitura por faixa e
percurso ordenado, que é exatamente o que um índice de banco relacional precisa.
A árvore B+ entrega isso com um único formato de página e sem compactação em
segundo plano.

**Log só de refazer, sem desfazer.** Uma página suja fica presa no pool de
buffers até a transação confirmar, então o arquivo de dados nunca contém dado não
confirmado. Isso dispensa registros de desfazer no log: a recuperação só precisa
reaplicar as transações que chegaram a gravar o registro de commit. Em compensação,
uma transação de escrita muito grande consome memória proporcional ao número de
páginas que ela tocou.

**Um escritor, vários leitores.** Transações de escrita são serializadas por um
mutex de leitura e escrita. É a garantia mais simples que ainda permite leitura
concorrente, e evita a complexidade de controle de versões múltiplas sem entregar
menos do que promete.

**Chave primária inteira vira a chave da linha.** Quando a tabela declara
`id INTEGER PRIMARY KEY`, esse valor é usado diretamente como chave na árvore da
tabela. A busca por chave primária lê uma árvore só, sem o segundo acesso que um
índice secundário exigiria.

**Codificação de chaves que preserva ordem.** Valores viram cadeias de bytes cuja
comparação byte a byte é igual à comparação semântica. É o que permite usar a
mesma árvore B+ como índice: inteiros com o bit de sinal invertido, ponto
flutuante com os bits ajustados para negativos, texto com o zero escapado e
terminador explícito.

## Limitações conhecidas

São escolhas de escopo, não defeitos escondidos:

- Só `INNER JOIN`. Não há `LEFT`, `RIGHT` nem `FULL OUTER JOIN`.
- Sem subconsultas, sem `UNION`, sem chaves estrangeiras, sem `ALTER TABLE`.
- Índices cobrem uma coluna só, não compostos.
- O resultado de um `SELECT` é materializado antes de a transação fechar, então
  consultas que devolvem muitas linhas ocupam memória proporcional ao resultado.
- O planejador usa heurísticas de forma, não estatísticas de cardinalidade.
- Nós subutilizados não são fundidos na remoção: uma folha só é liberada quando
  fica vazia.

## Testes

```sh
make test      # go test -race ./...
make cover     # relatório de cobertura em coverage.html
make bench     # benchmarks
make fuzz      # 30 segundos de fuzzing no parser
```

A suíte cobre, entre outras coisas: uma carga aleatória na árvore B+ comparada
contra um mapa em memória, valores de 40 KB em páginas de transbordo, ida e volta
da codificação de chaves e linhas, precedência de operadores no parser, e
recuperação depois de uma falha simulada (o processo abandona o banco sem
checkpoint e reabre o arquivo).

Números de referência em uma máquina de 4 núcleos, com `NoSync` ligado:

| Operação | Tempo |
| --- | --- |
| Inserção na árvore B+ | 1,0 µs |
| `INSERT` via SQL | 2,6 µs |
| `SELECT` por índice | 8,9 µs |

## Licença

MIT. Veja [LICENSE](LICENSE).
