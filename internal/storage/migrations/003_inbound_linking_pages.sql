-- inbound_linking_pages tracks the number of DISTINCT source pages that
-- link to each page, in contrast to inbound_edge_count which counts every
-- <a href> instance (so a page reached from header + footer + body of
-- 200 pages contributes 600 to inbound_edge_count but ~200 to
-- inbound_linking_pages).
ALTER TABLE pages ADD COLUMN inbound_linking_pages INTEGER NOT NULL DEFAULT 0;
