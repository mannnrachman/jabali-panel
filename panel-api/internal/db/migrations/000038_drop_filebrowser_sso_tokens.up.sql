-- M11 filebrowser decommission (ADR-0030). The AntD-native file manager
-- (panel-ui/src/shells/user/files/FileManagerPage.tsx) uses the panel's
-- own JWT — no SSO token table needed for files. Drop is IF EXISTS so
-- fresh installs that never ran 000034 don't fail.
DROP TABLE IF EXISTS filebrowser_sso_tokens;
