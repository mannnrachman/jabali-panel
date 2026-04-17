// User shell wrapper over DatabaseUserCreate. The admin component is
// shell-agnostic (uses relative navigation + the claim-aware backend),
// so this is a pure re-export for the user shell's resource config.
import { DatabaseUserCreate } from "../../admin/database-users/DatabaseUserCreate";

export const UserDatabaseUserCreate = () => <DatabaseUserCreate />;
