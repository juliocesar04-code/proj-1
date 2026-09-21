-- Uma loja pequena, para exercitar o que o banco sabe fazer.
-- Rode com:  corvo -f exemplos/loja.sql exemplos/loja.db

CREATE TABLE usuarios (
    id     INTEGER PRIMARY KEY,
    nome   TEXT NOT NULL,
    email  TEXT UNIQUE,
    cidade TEXT,
    ativo  BOOLEAN
);

CREATE TABLE pedidos (
    id         INTEGER PRIMARY KEY,
    usuario_id INTEGER NOT NULL,
    total      FLOAT NOT NULL,
    status     TEXT NOT NULL
);

INSERT INTO usuarios (nome, email, cidade, ativo) VALUES
    ('Ana Ribeiro',    'ana@exemplo.com',    'Curitiba',   TRUE),
    ('Bruno Tavares',  'bruno@exemplo.com',  'São Paulo',  TRUE),
    ('Carla Menezes',  'carla@exemplo.com',  'Curitiba',   FALSE),
    ('Diego Alves',    NULL,                 'Recife',     TRUE),
    ('Elisa Prado',    'elisa@exemplo.com',  'São Paulo',  TRUE);

INSERT INTO pedidos (usuario_id, total, status) VALUES
    (1, 120.50, 'pago'),
    (1,  49.90, 'pago'),
    (1,  80.10, 'cancelado'),
    (2, 310.00, 'pendente'),
    (2,  59.90, 'pago'),
    (3,  15.00, 'pago'),
    (5, 240.00, 'pago'),
    (5,  18.75, 'pendente');

CREATE INDEX idx_pedidos_status ON pedidos (status);
CREATE INDEX idx_usuarios_cidade ON usuarios (cidade);

-- Quanto cada cliente ativo já gastou em pedidos pagos.
SELECT u.nome,
       COUNT(*)                AS pedidos,
       ROUND(SUM(p.total), 2)  AS gasto
FROM usuarios u
JOIN pedidos p ON p.usuario_id = u.id
WHERE p.status = 'pago' AND u.ativo = TRUE
GROUP BY u.nome
ORDER BY gasto DESC;

-- Cidades com mais de um cliente.
SELECT cidade, COUNT(*) AS clientes
FROM usuarios
GROUP BY cidade
HAVING COUNT(*) > 1
ORDER BY clientes DESC, cidade;

-- O índice de status é usado no lugar da varredura completa.
EXPLAIN SELECT id, total FROM pedidos WHERE status = 'pendente';

SELECT id, total FROM pedidos WHERE status = 'pendente' ORDER BY total DESC;

-- Ticket médio e extremos, ignorando os cancelados.
SELECT COUNT(*)                AS pedidos,
       ROUND(AVG(total), 2)    AS ticket_medio,
       MIN(total)              AS menor,
       MAX(total)              AS maior
FROM pedidos
WHERE status != 'cancelado';

-- Transação: se algo der errado no meio, nada fica gravado.
BEGIN;
UPDATE pedidos SET status = 'pago' WHERE status = 'pendente';
SELECT status, COUNT(*) AS quantos FROM pedidos GROUP BY status ORDER BY status;
ROLLBACK;

SELECT status, COUNT(*) AS quantos FROM pedidos GROUP BY status ORDER BY status;

-- Clientes sem email cadastrado.
SELECT nome, cidade FROM usuarios WHERE email IS NULL;

-- Busca por padrão e faixa de valores.
SELECT nome FROM usuarios WHERE nome LIKE '%a%' ORDER BY nome;
SELECT id, total FROM pedidos WHERE total BETWEEN 50 AND 250 ORDER BY total;
