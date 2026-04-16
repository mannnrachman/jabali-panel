-- Rewrite domain docroots from the old /home/<user>/public_html/<name>
-- layout to the new /home/<user>/domains/<name>/public_html layout.
-- Only rewrites rows that match the old convention exactly so manually
-- customised docroots are preserved.
UPDATE domains d
  JOIN users u ON u.id = d.user_id
   SET d.doc_root = CONCAT('/home/', u.username, '/domains/', d.name, '/public_html')
 WHERE u.username IS NOT NULL
   AND d.doc_root = CONCAT('/home/', u.username, '/public_html/', d.name);
