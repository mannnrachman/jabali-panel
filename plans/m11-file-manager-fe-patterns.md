# File Manager Frontend Patterns & Implementation Guide

**Status:** Cold implementation reference  
**Target Audience:** Frontend developer building file manager UI from scratch  
**Scope:** Panel-UI patterns for consistency with cron, databases, SSH keys  

---

## 1. User Shell Scaffolding & Routing

### How routes are registered

Routes are **statically defined in `/panel-ui/src/App.tsx`** using Refine's `resources` config + React Router:

**File:** `/home/shuki/projects/jabali2/panel-ui/src/App.tsx` (lines ~147–198)

```tsx
{
  name: "files",
  list: "/jabali-panel/files",
  meta: { label: "Files", icon: <FolderOutlined />, shell: "user" },
},
```

**Route binding:**
```tsx
<Route path="files" element={<UserFilesLauncher />} />
```

**Pattern for file manager:** Replace `UserFilesLauncher` with `UserFilesPage` once we build the native UI:
- Resource name: `"files"` (matches API slug for dataProvider calls)
- Route path: `/jabali-panel/files` (absolute) → `files` (relative under user shell)
- Meta icon: Already set to `<FolderOutlined />`
- Meta label: Already set to `"Files"`

**Breadcrumb:** The shell auto-generates breadcrumbs via `location.pathname` matching against `resources[].list`.

---

## 2. TanStack Query Hooks: Query Keys & Patterns

### Query Key Convention

All query keys use a **flat array, single string + params** pattern:

**Cron example** (`/panel-ui/src/shells/user/cron/UserCronList.tsx` line 65):
```tsx
const { data: listResponse = { items: [] }, isLoading, refetch } = useQuery({
  queryKey: ["cron-jobs"],
  queryFn: async () => listCronJobs(),
});
```

**SSH Keys example** (`/panel-ui/src/shells/user/ssh-keys/UserSSHKeysPage.tsx` line 36):
```tsx
const { data: listResponse = { items: [] }, isLoading, refetch } = useQuery({
  queryKey: ["ssh-keys"],
  queryFn: async () => listSSHKeys(),
});
```

**Pattern for file manager:**
```tsx
// List all files in current folder
const { data: listResponse = { items: [], folders: [] }, isLoading, refetch } = useQuery({
  queryKey: ["files", currentFolderId],
  queryFn: async () => listFiles(currentFolderId),
});
```

### Mutations & Invalidation

**SSL Manager example** (`/panel-ui/src/components/ssl/SSLManagerTable.tsx` lines 58–70):
```tsx
const queryClient = useQueryClient();

const renewMutation = useMutation({
  mutationFn: async (domainId: string) => {
    await apiClient.post(`/domains/${domainId}/ssl/renew`);
  },
  onSuccess: () => {
    message.success("Certificate renewal initiated");
    queryClient.invalidateQueries({ queryKey: ["ssl-manager", endpoint] });
  },
  onError: (error: unknown) => {
    const msg = (error as any)?.message ?? "Failed to renew";
    message.error(msg);
  },
});
```

**Pattern for file manager mutations:**
```tsx
const uploadMutation = useMutation({
  mutationFn: async (files: File[]) => {
    return await uploadFiles(currentFolderId, files);
  },
  onSuccess: () => {
    message.success("Files uploaded successfully");
    queryClient.invalidateQueries({ queryKey: ["files", currentFolderId] });
  },
  onError: (error: unknown) => {
    const msg = (error as any)?.message ?? "Upload failed";
    message.error(msg);
  },
});
```

---

## 3. AntD Table Usage: Columns, Row Actions, Pagination

### Column Definition Pattern

**Cron example** (`/panel-ui/src/shells/user/cron/UserCronList.tsx` lines 160–210):
```tsx
<Table<CronJob>
  dataSource={jobs}
  loading={isLoading_}
  rowKey="id"
  bordered
  pagination={false}  // false = no pagination (single page list)
  columns={[
    {
      title: "Name",
      dataIndex: "name",
      sorter: (a, b) => a.name.localeCompare(b.name),
      defaultSortOrder: "ascend",
    },
    {
      title: "Last Run",
      dataIndex: "last_run_at",
      render: (lastRunAt: string | null) =>
        lastRunAt ? dayjs(lastRunAt).fromNow() : "Never",
    },
    {
      title: "Actions",
      dataIndex: "actions",
      render: (_, record) => (
        <Space size="small">
          <Button type="text" size="small" onClick={() => handleRunNow(record)}>
            Run now
          </Button>
          <Popconfirm
            title="Delete?"
            onConfirm={() => handleDelete(record)}
          >
            <Button type="text" danger size="small">
              Delete
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]}
/>
```

### Database List with SearchableTable

**Databases example** (`/panel-ui/src/shells/user/databases/UserDatabaseList.tsx` lines 114–172):
```tsx
<SearchableTable<Database>
  {...tableProps}
  rowKey="id"
  bordered
  initialSearch={initialSearch}
  searchPlaceholder="Search by database name"
  onSearchChange={(filters) => setFilters(filters, "replace")}
>
  <Table.Column<Database>
    dataIndex="name"
    title="Database"
    sorter={{ multiple: 1 }}
    defaultSortOrder="ascend"
  />
  <Table.Column<Database>
    dataIndex="engine"
    title="Engine"
    render={(engine: string) => (
      <Tag color={engineColorMap[engine] || "default"}>{engine}</Tag>
    )}
  />
  <Table.Column<Database>
    title="Actions"
    render={(_, r) => (
      <Space size="small">
        <Button type="link" size="small" onClick={() => handleAction(r)}>
          Action
        </Button>
      </Space>
    )}
  />
</SearchableTable>
```

### Pagination Defaults

**SearchableTable** (`/panel-ui/src/components/SearchableTable.tsx` lines 67–77):
```tsx
const mergedPagination = useMemo(() => {
  if (pagination === false) return false as const;
  return {
    showSizeChanger: true,
    pageSizeOptions: ["10", "20", "50", "100"],
    showTotal: (total: number, range: [number, number]) =>
      `${range[0]}–${range[1]} of ${total}`,
    ...(pagination ?? {}),
  };
}, [pagination]);
```

**Pattern for file manager table:** Use `SearchableTable` with 10-item default page size, `showSizeChanger: true`.

---

## 4. AntD Tree Usage: Folder Hierarchy

### No existing Tree usage in codebase

**Tree is greenfield.** Recommended pattern based on AntD docs:

```tsx
import { Tree, TreeProps } from "antd";

interface FileTreeNode {
  key: string;  // folder ID
  title: string;  // folder name
  children?: FileTreeNode[];
  isLeaf?: boolean;  // no children
}

export const FolderTree = ({ 
  root, 
  onSelect, 
  expandedKeys, 
  setExpandedKeys 
}: FolderTreeProps) => {
  const [loading, setLoading] = useState(false);

  const handleExpand: TreeProps["onExpand"] = (expandedKeysValue) => {
    setExpandedKeys(expandedKeysValue);
  };

  // Lazy-load children when folder is expanded
  const handleLoadData: TreeProps["loadData"] = async (node) => {
    if (node.children?.length) return;  // Already loaded
    setLoading(true);
    try {
      const children = await fetchFolderContents(node.key as string);
      node.children = children.map(f => ({
        key: f.id,
        title: f.name,
        isLeaf: false,
      }));
      setExpandedKeys(prev => [...prev, node.key as string]);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Tree
      treeData={[root]}
      expandedKeys={expandedKeys}
      onExpand={handleExpand}
      loadData={handleLoadData}
      onSelect={(selectedKeys) => onSelect(selectedKeys[0] as string)}
      defaultExpandAll={false}
      blockNode
    />
  );
};
```

**Key decisions:**
- **Controlled expansion:** `expandedKeys` state + `onExpand` callback
- **Lazy loading:** `loadData` prop for async folder fetch
- **Selection:** `onSelect` fires on single-click; store selected folder ID in parent state
- **Root folder:** Expand root by default; leave others collapsed

---

## 5. AntD Upload: File Upload with Progress & Validation

### No existing Upload component in user shell

**Recommended pattern for file manager:**

```tsx
import { Upload, Button, message, UploadProps, UploadFile } from "antd";
import { CloudUploadOutlined } from "@ant-design/icons";

interface UploadResponse {
  file_id: string;
  name: string;
}

export const UploadButton = ({ 
  folderId, 
  onSuccess 
}: UploadButtonProps) => {
  const [fileList, setFileList] = useState<UploadFile[]>([]);

  const handleCustomRequest: UploadProps["customRequest"] = async (options) => {
    const { file, onSuccess: onUploadSuccess, onError } = options;
    const formData = new FormData();
    formData.append("file", file as File);
    formData.append("folder_id", folderId);

    try {
      const response = await apiClient.post<UploadResponse>(
        "/files/upload",
        formData,
        {
          headers: { "Content-Type": "multipart/form-data" },
          onUploadProgress: (progressEvent) => {
            // Use native browser progress if available
            if (progressEvent.total) {
              options.onProgress?.({
                percent: Math.round((progressEvent.loaded / progressEvent.total) * 100),
              });
            }
          },
        }
      );
      onUploadSuccess?.(response.data);
      message.success(`${file.name} uploaded`);
      onSuccess?.();
    } catch (error: unknown) {
      const msg = (error as any)?.message ?? "Upload failed";
      onError?.(new Error(msg));
      message.error(msg);
    }
  };

  const beforeUpload = (file: File) => {
    const MAX_SIZE = 500 * 1024 * 1024;  // 500 MB
    if (file.size > MAX_SIZE) {
      message.error(`File must be ≤ 500 MB`);
      return false;
    }
    return true;
  };

  return (
    <Upload
      customRequest={handleCustomRequest}
      beforeUpload={beforeUpload}
      multiple
      onRemove={() => setFileList([])}
      fileList={fileList}
      onChange={(info) => setFileList(info.fileList)}
    >
      <Button icon={<CloudUploadOutlined />}>
        Upload Files
      </Button>
    </Upload>
  );
};
```

**Key decisions:**
- **customRequest:** Pass folder ID + file in FormData; use axios directly for multipart
- **Progress:** `onUploadProgress` from axios
- **Validation:** `beforeUpload` for size checks
- **Error UX:** Catch error, show `message.error`, pass to `onError` callback
- **File size limit:** 500 MB recommended (adjust per backend policy)

---

## 6. Modal Patterns: Create, Rename, Delete

### Create/Edit Modal

**Cron example** (`/panel-ui/src/shells/user/cron/CreateCronModal.tsx` lines 58–125):
```tsx
export const CreateCronModal = ({
  open,
  onClose,
  onSuccess,
  initial,
}: CreateCronModalProps) => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (values: { name: string; command: string }) => {
    setLoading(true);
    try {
      if (initial) {
        await updateCronJob(initial.id, values);
        message.success("Updated");
      } else {
        await createCronJob(values);
        message.success("Created");
      }
      form.resetFields();
      onSuccess();
    } catch (error: unknown) {
      const msg = (error as any)?.message ?? "Failed";
      message.error(msg);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      title={initial ? "Edit Cron Job" : "Create Cron Job"}
      open={open}
      onCancel={onClose}
      footer={null}
      width={600}
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        initialValues={{
          name: initial?.name || "",
          command: initial?.command || "",
        }}
      >
        <Form.Item
          label="Name"
          name="name"
          rules={[{ required: true, message: "Required" }]}
        >
          <Input placeholder="e.g., Backup" />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Space>
            <Button type="primary" htmlType="submit" loading={loading}>
              {initial ? "Update" : "Create"}
            </Button>
            <Button onClick={onClose}>Cancel</Button>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
};
```

**Pattern for file manager create/rename modals:**
- Use `Form.useForm()` for form state
- Set `loading` during submit
- Reset form + close on success
- Render error via `message.error`
- Show loading spinner on submit button via `loading={loading}`

### Delete Confirmation Modal

**Cron example** (`/panel-ui/src/shells/user/cron/UserCronList.tsx` lines 285–299):
```tsx
const handleDelete = async (job: CronJob) => {
  setDeletingId(job.id);
  try {
    await deleteCronJob(job.id);
    message.success("Deleted");
    refetch();
  } catch (error) {
    message.error((error as any)?.message ?? "Failed");
  } finally {
    setDeletingId(null);
  }
};

// In table row actions:
<Popconfirm
  title="Delete Cron Job"
  description="Are you sure you want to delete this cron job?"
  onConfirm={() => handleDelete(record)}
  okText="Yes"
  cancelText="No"
>
  <Button
    type="text"
    danger
    size="small"
    icon={<DeleteOutlined />}
    loading={deletingId === record.id}
  >
    Delete
  </Button>
</Popconfirm>
```

**Pattern:** Use `Popconfirm` (inline) for destructive actions; show loading on button during delete.

---

## 7. Dropdown / Context Menu Pattern

### No existing right-click menus in user shell

**Recommended pattern for file manager:**

```tsx
import { Dropdown, Button, MenuProps } from "antd";
import { MoreOutlined, CopyOutlined, EditOutlined, DeleteOutlined } from "@ant-design/icons";

interface FileRowActionsProps {
  file: FileItem;
  onRename: (file: FileItem) => void;
  onDelete: (file: FileItem) => void;
}

export const FileRowActions = ({ file, onRename, onDelete }: FileRowActionsProps) => {
  const items: MenuProps["items"] = [
    {
      key: "rename",
      icon: <EditOutlined />,
      label: "Rename",
      onClick: () => onRename(file),
    },
    {
      key: "copy",
      icon: <CopyOutlined />,
      label: "Copy Path",
      onClick: () => {
        navigator.clipboard.writeText(file.path);
        message.success("Copied");
      },
    },
    {
      type: "divider",
    },
    {
      key: "delete",
      icon: <DeleteOutlined />,
      label: "Delete",
      danger: true,
      onClick: () => onDelete(file),
    },
  ];

  return (
    <Dropdown menu={{ items }} trigger={["click"]}>
      <Button type="text" size="small" icon={<MoreOutlined />} />
    </Dropdown>
  );
};
```

**For right-click context menu,** use `trigger={["contextMenu"]}` and prevent default:
```tsx
<div
  onContextMenu={(e) => {
    e.preventDefault();
    // Dropdown handles the rest
  }}
>
  <Dropdown menu={{ items }} trigger={["contextMenu"]}>
    <div>{/* file content */}</div>
  </Dropdown>
</div>
```

---

## 8. Breadcrumb Navigation Pattern

### Current breadcrumb behavior

The layout auto-generates breadcrumbs from `location.pathname` + resource definitions. **No manual breadcrumb component needed** for simple top-level pages like `/jabali-panel/files`.

For **nested file browsing** (e.g., `/jabali-panel/files/folder/subfolder`), implement a custom breadcrumb:

```tsx
import { Breadcrumb, Button } from "antd";
import { HomeOutlined, FolderOutlined } from "@ant-design/icons";

interface FileBreadcrumbProps {
  path: string[];  // ['root', 'folder1', 'folder2']
  onNavigate: (index: number) => void;
}

export const FilesBreadcrumb = ({ path, onNavigate }: FileBreadcrumbProps) => {
  const items = [
    {
      title: <HomeOutlined />,
      onClick: () => onNavigate(0),
    },
    ...path.map((name, idx) => ({
      title: name,
      onClick: () => onNavigate(idx + 1),
    })),
  ];

  return <Breadcrumb items={items} style={{ marginBottom: 16 }} />;
};
```

**Separator:** AntD uses `>` by default (no customization needed).  
**Truncation:** If path exceeds 5 levels, use `…` to collapse middle items.

---

## 9. Dark Mode Token Support

### Theme token definition

**File:** `/home/shuki/projects/jabali2/panel-ui/src/muiTheme.ts` (lines 1–150)

```tsx
const lightTokens: AliasToken = {
  colorPrimary: "#1890ff",
  colorBgBase: "#ffffff",
  colorTextBase: "#000000",
  colorBorder: "#d9d9d9",
  // ... 100+ more tokens
};

const darkTokens: AliasToken = {
  colorPrimary: "#177ddc",
  colorBgBase: "#141414",
  colorTextBase: "#ffffffd9",
  colorBorder: "#434343",
  // ... more tokens
};

const useMuiTheme = (mode: ThemeMode) => {
  return useMemo<ConfigProviderProps>(
    () => ({
      theme: {
        algorithm:
          mode === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: mode === "dark" ? darkTokens : lightTokens,
        components: mode === "dark" ? darkComponents : lightComponents,
      },
    }),
    [mode],
  );
};
```

### How to use dark mode in new components

**All AntD components automatically respect theme tokens.** For custom styling:

```tsx
import { useToken } from "antd/theme";

export const CustomFilePanel = () => {
  const { token } = useToken();

  return (
    <div
      style={{
        backgroundColor: token.colorBgBase,
        color: token.colorTextBase,
        border: `1px solid ${token.colorBorder}`,
        padding: token.padding,
      }}
    >
      Content
    </div>
  );
};
```

**Token names to use:**
- `colorBgBase` → main background
- `colorTextBase` → main text
- `colorBorder` → borders, dividers
- `colorPrimary` → CTA buttons
- `colorBgElevated` → modals, drawers
- `colorErrorBg` / `colorErrorText` → errors

**DO NOT hardcode colors.** Always use `useToken()`.

---

## 10. Admin Impersonation Behavior

### How impersonation works

When an admin impersonates a user, they log in via a **one-shot session token** that the UI fetches via `/admin/users/:id/impersonate`:

**File:** `/home/shuki/projects/jabali2/panel-ui/src/shells/admin/users/UserImpersonateAction.tsx` (lines 17–28)

```tsx
const handleImpersonate = async () => {
  try {
    const resp = await apiClient.post<{ login_url: string }>(
      `/admin/users/${encodeURIComponent(recordItemId)}/impersonate`
    );
    message.success(`Opening login link for ${userEmail}`);
    window.open(resp.data.login_url, "_blank");
  } catch (err) {
    message.error("Failed to impersonate user");
  }
};
```

### File manager behavior under impersonation

**The file manager inherits this behavior automatically:**
- When admin opens a new tab with the impersonation login URL, they're logged in as that user
- All API calls are scoped to the impersonated user's files (no explicit handling needed)
- No "acting as X" banner is currently rendered (future M5a feature)

**No special code needed** in file manager—the auth token/session already restricts access.

---

## 11. Error & Toast Patterns

### Standard error display: `message.error()`

**Used throughout codebase** for inline errors:

```tsx
import { message } from "antd";

try {
  await apiCall();
} catch (error) {
  const msg = (error as any)?.message ?? "Operation failed";
  message.error(msg);
}
```

### Error handling hierarchy

1. **Backend detail message** (preferred): `error.response.data.detail`
2. **Backend error code** (mapped): `error.response.data.error` (map to user-friendly text)
3. **Axios message**: `error.message`
4. **Generic fallback**: `"Operation failed"`

**Cron example** (`/panel-ui/src/shells/user/cron/CreateCronModal.tsx` lines 77–99):
```tsx
catch (error: unknown) {
  const err = error as any;
  const detail = err?.response?.data?.detail;
  if (detail) {
    if (detail.includes("command")) {
      form.setFields([{ name: "command", errors: [detail] }]);
      message.error("Invalid command");
    } else {
      message.error(detail);
    }
  } else {
    const msg = err?.message ?? "Failed to save";
    message.error(msg);
  }
}
```

### Toast vs. Notification

- **`message.error/success/info`** → centered, auto-dismiss, for brief feedback
- **`notification.error`** → top-right corner, persistent, for long messages (not used in user shell)

**Pattern:** Use `message.*` for all user shell notifications.

---

## 12. Loading States: Spin & Button.loading

### Full-page loading

```tsx
const { isLoading } = useQuery({ ... });

if (isLoading) {
  return (
    <div style={{ textAlign: "center", padding: "40px" }}>
      <Spin />
    </div>
  );
}
```

### Inline table loading

```tsx
<Table loading={isLoading_} dataSource={data} ... />
```

### Button loading during mutation

```tsx
const mutation = useMutation({ ... });

<Button loading={mutation.isPending} onClick={() => mutation.mutate()}>
  Submit
</Button>
```

**Pattern:** Never block the entire page unless data is truly unavailable. Use inline spinners for sections.

---

## Recommended File Structure

Based on cron/databases patterns:

```
panel-ui/src/shells/user/files/
├── UserFilesPage.tsx           # Main container, Layout (Tree + Table + Upload)
├── FolderTree.tsx              # Folder hierarchy (left sidebar)
├── FileTable.tsx               # File/folder list table
├── FilesBreadcrumb.tsx         # Navigation breadcrumb
├── UploadButton.tsx            # Upload button wrapper
├── FileRowActions.tsx          # Row menu (rename, delete, copy path)
├── CreateFolderModal.tsx       # New folder form
├── RenameModal.tsx             # Rename file/folder form
├── DeleteConfirmModal.tsx      # Delete confirmation (Popconfirm or Modal)
├── PreviewDrawer.tsx           # File preview sidebar
├── hooks/
│   ├── useFilesList.ts         # useQuery([files, folderId])
│   ├── useFilesUpload.ts       # useMutation for upload
│   └── useFilesMutations.ts    # useMutation for create/rename/delete
└── types.ts                    # FileItem, FolderItem, etc.
```

### File organization notes

- **Container** (`UserFilesPage.tsx`): Layout, folder state, folder selection
- **Sub-components:** One per concern (tree, table, modals, buttons)
- **Hooks:** Extract TanStack Query logic; reusable across components
- **Types:** Shared interface definitions (avoid duplicating API types)

---

## Quick Start Checklist

- [ ] Add `<Route path="files" element={<UserFilesPage />} />` to App.tsx (already exists; just swap component)
- [ ] Implement `useFilesList(folderId)` hook with query key `["files", folderId]`
- [ ] Build `FolderTree` with lazy-load + controlled expansion
- [ ] Build `FileTable` using `SearchableTable` with 10-item pagination
- [ ] Build modals using Ant Form + Modal (copy CreateCronModal structure)
- [ ] Use `useToken()` for dark mode compliance
- [ ] All errors go through `message.error(msg)`
- [ ] All mutations invalidate `["files", folderId]` on success
- [ ] Test impersonation flow (admin → user → files page)

---

## References

**API Client:** `/home/shuki/projects/jabali2/panel-ui/src/apiClient.ts`  
**Theme:** `/home/shuki/projects/jabali2/panel-ui/src/muiTheme.ts`  
**Cron (reference):** `/home/shuki/projects/jabali2/panel-ui/src/shells/user/cron/`  
**Databases (reference):** `/home/shuki/projects/jabali2/panel-ui/src/shells/user/databases/`  
**SSH Keys (reference):** `/home/shuki/projects/jabali2/panel-ui/src/shells/user/ssh-keys/`  
**SearchableTable:** `/home/shuki/projects/jabali2/panel-ui/src/components/SearchableTable.tsx`
