// User shell wrapper over the admin DatabaseUsersList. The component
// itself is scope-aware (the backend filters by claims), so we only
// need to reframe the title.
//
// The only user-shell-specific bit is the "Create" redirect target —
// handled inside DatabaseUserCreate via the shell-specific wrapper.
import { DatabaseUsersList } from "../../admin/database-users/DatabaseUsersList";

export const UserDatabaseUsersList = () => <DatabaseUsersList />;
