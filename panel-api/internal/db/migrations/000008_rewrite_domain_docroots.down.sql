-- Best-effort revert. Same guard: only rewrites rows that currently
-- match the new layout.
UPDATE domains d
  JOIN users u ON u.id = d.user_id
   SET d.doc_root = CONCAT('/home/', u.username, '/public_html/', d.name)
 WHERE u.username IS NOT NULL
   AND d.doc_root = CONCAT('/home/', u.username, '/domains/', d.name, '/public_html');
