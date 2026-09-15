PRAGMA user_version = 20221111;

CREATE TABLE book (
    id integer PRIMARY KEY AUTOINCREMENT,
    title text,
    authors text,
    notes integer,
    last_open integer,
    highlights integer,
    pages integer,
    series text,
    language text,
    md5 text,
    total_read_time integer,
    total_read_pages integer
);

CREATE UNIQUE INDEX book_title_authors_md5 ON book(title, authors, md5);

CREATE TABLE page_stat_data (
    id_book integer,
    page integer NOT NULL DEFAULT 0,
    start_time integer NOT NULL DEFAULT 0,
    duration integer NOT NULL DEFAULT 0,
    total_pages integer NOT NULL DEFAULT 0,
    UNIQUE (id_book, page, start_time),
    FOREIGN KEY(id_book) REFERENCES book(id)
);

CREATE INDEX page_stat_data_start_time ON page_stat_data(start_time);

CREATE TABLE numbers (number INTEGER PRIMARY KEY);
INSERT INTO numbers SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3;

CREATE VIEW page_stat AS
SELECT id_book, page, start_time, duration
FROM page_stat_data;

INSERT INTO book VALUES
    (1, 'Reading One', 'Author One', 0, 1726200100, 2, 300, NULL, 'en', 'book-one', 120, 2),
    (2, 'Unopened', NULL, 0, 0, 0, 100, NULL, 'en', 'book-two', 0, 0);

INSERT INTO page_stat_data VALUES
    (1, 3, 1726200000, 60, 300),
    (1, 4, 1726200060, 0, 300);
