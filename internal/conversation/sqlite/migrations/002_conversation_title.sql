ALTER TABLE conversations
ADD COLUMN title TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 80);
