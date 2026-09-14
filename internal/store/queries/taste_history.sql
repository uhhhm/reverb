-- name: InsertTasteHistory :exec
INSERT INTO taste_history (provider, artist, title, plays, at)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteTasteHistory :exec
DELETE FROM taste_history
WHERE provider = ?;

-- name: ListTasteHistory :many
SELECT provider, artist, title, plays, at
FROM taste_history
ORDER BY rowid;
