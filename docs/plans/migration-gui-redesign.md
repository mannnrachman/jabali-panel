# Jabali Panel - Migration GUI Redesign Plan

**Document Type:** Product Requirements Document (PRD)  
**Feature:** M35 - Account Migration Importer GUI  
**Status:** Draft - Ready for Review  
**Date:** 2026-05-12

---

## 1. Executive Summary

### Current State
The migration GUI is a read-only table view with a basic creation drawer. It's developer-focused, uses technical jargon, and lacks user-friendly guidance.

### Proposed Solution
A 4-step wizard flow that guides operators through selecting source type, connecting to the server, choosing what to migrate, and reviewing before starting. Progress is shown with visual cards showing each stage.

### Goals
- Make migration accessible to non-technical operators
- Support all source types (WHM, cPanel, cpmove, DirectAdmin, HestiaCP)
- Provide clear progress visualization
- Work on mobile and tablet devices
- Reduce support tickets by making the flow self-explanatory

---

## 2. Current Problems Analysis

### 2.1 Information Architecture Issues
- **Technical alert banner** dominates the page mentioning "JMAP-push", "per-area-builder", and markdown files
- **"Per-source-kind support" card** shows internal development statuses like "Discoverer scaffold only" and "Not yet wired"
- **No filtering** - can't see running vs completed migrations
- **Table missing critical info** - no target user, no progress percentage, no size

### 2.2 User Experience Issues
- **Empty state** just says "No migrations yet" with no call-to-action
- **States are technical** - "fix_perms", "analyzing" instead of user-friendly labels
- **No visual progress** - just text status tags
- **Table overflows** on mobile - 7 columns don't fit on 390px screens

### 2.3 Missing Features
- Can't see migration size or duration
- Can't filter by status or source type
- No bulk actions
- No retry button for failed migrations
- No preview of what will be migrated before starting

---

## 3. User Flow

### 3.1 Main Flow - Creating a New Migration

```
[Migration List Page]
    │
    ├── Empty State → [Start Your First Migration Button]
    │
    └── Has Jobs → [+ New Migration Button]
                    │
                    ▼
[Step 1: Select Source Type]
    │
    ├── WHM (Full Server)
    ├── cPanel (Single Account)
    ├── cPanel Move File
    ├── DirectAdmin
    └── HestiaCP
                    │
                    ▼
[Step 2: Connection]
    │
    ├── Live Server → Server credentials form
    │   ├── Host, Port, Username, Password/API Token
    │   └── SSL toggle
    │
    └── File Upload → Drag & drop file upload
        └── Supports .tar, .tar.gz
                    │
                    ▼
[Step 3: Select Items]
    │
    ├── WHM/DirectAdmin/HestiaCP → Account selection list
    │   ├── Fetch accounts from server
    │   ├── Show: username, domain, size
    │   └── Checkbox selection
    │
    └── cPanel/cpmove → Item selection grid
        ├── Home Directory
        ├── Databases
        ├── Email Accounts
        ├── DNS Zones
        ├── SSL Certificates
        └── Cron Jobs
                    │
                    ▼
[Step 4: Review & Confirm]
    │
    ├── Summary card showing:
    │   ├── Source type and server
    │   ├── Selected accounts (count)
    │   ├── Selected items (icons)
    │   └── Estimated size
    │
    └── [Start Migration] button
                    │
                    ▼
[Migration List Page]
    └── Shows new migration card with progress
```

### 3.2 Alternative Flow - Viewing Progress

```
[Migration List Page]
    │
    ├── Stats Bar (Total / Running / Completed / Failed)
    │
    ├── Filter Bar (Search + Status + Source Type)
    │
    └── Migration Cards
        ├── Click card → [Detail Page]
        │   ├── Stage timeline (4 stages)
        │   ├── Per-stage progress bars
        │   ├── Log output
        │   └── Actions (Cancel / Retry / Destroy)
        │
        └── Actions on card
            ├── Cancel (if running)
            └── Destroy (if done/failed)
```

---

## 4. Screen Specifications

### 4.1 Migration List Page

#### Header Section
```
[Icon] Account Migrations
Migrate accounts from cPanel, WHM, DirectAdmin, and other hosting panels.

[+ New Migration]  [Refresh]
```

#### Stats Bar
```
┌──────────────┬──────────────┬──────────────┬──────────────┐
│   12 Total   │  2 Running   │  9 Completed │   1 Failed   │
└──────────────┴──────────────┴──────────────┴──────────────┘
```
- **Clickable filters**: Clicking a stat filters the list
- **Colors**: Running=blue, Completed=green, Failed=red
- **Mobile**: Stack vertically or 2x2 grid

#### Filter Bar
```
[Search by user or host...              ] [Status: All ▼] [Source: All ▼]
```
- **Search**: Filters by source_user, source_host, target_user
- **Status filter**: All, Pending, Running, Completed, Failed, Cancelled
- **Source filter**: All, WHM, cPanel, cpmove, DirectAdmin, HestiaCP

#### Empty State
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│                    [Migration Illustration]                 │
│                                                             │
│                    No migrations yet                        │
│                                                             │
│   Import accounts from other hosting panels to migrate      │
│   users, domains, databases, emails, and files              │
│   automatically.                                            │
│                                                             │
│              [ + Start Your First Migration ]               │
│                                                             │
│              Supported: cPanel, WHM, DirectAdmin            │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

#### Migration Job Card
```
┌─────────────────────────────────────────────────────────────┐
│ [cPanel Icon]  cPanel Server                                │
│ From: root@192.168.100.168                                    │
│ To: laundryshco                                             │
│                                                             │
│ [████████████████████░░░░░░░░░░] 65%                        │
│ Restoring data...  •  752 MB  •  2 hours ago                │
│                                                             │
│ [View Details]  [Cancel]                                    │
└─────────────────────────────────────────────────────────────┘
```

**Card States:**
- **Running**: Blue progress bar, spinning icon, "Cancel" button
- **Completed**: Green checkmark, full progress bar, "Destroy" button
- **Failed**: Red X icon, "Retry" and "Destroy" buttons
- **Pending**: Gray, "Cancel" button

**Card Elements:**
- Source type icon + label
- Source server (host)
- Target user (where it's migrating TO)
- Progress bar with percentage
- Current stage label
- Size
- Duration / time ago
- Action buttons

### 4.2 Create Migration Wizard

#### Wizard Layout
```
┌─────────────────────────────────────────────────────────────┐
│ [← Back]  New Migration                          [Cancel]   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   [1 Source] → [2 Connect] → [3 Select] → [4 Review]       │
│    ●           ○           ○           ○                   │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│                    [Step Content]                           │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│              [         Continue         ]                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

#### Step 1: Select Source Type

**Header:** "Choose your source platform"
**Subtext:** "Select the hosting panel you're migrating from"

```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  [🏢 Icon]  WHM (Full Server)                    [Ready]    │
│  Import all accounts from a WHM/cPanel server               │
│  using the WHM API                                          │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  [👤 Icon]  cPanel (Single Account)              [Ready]    │
│  Import a single account via SSH using cPanel UAPI          │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  [📦 Icon]  cPanel Move File                     [Ready]    │
│  Upload a cpmove tarball file. No live server              │
│  connection needed                                          │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  [🔧 Icon]  DirectAdmin                          [Beta]     │
│  Import accounts from a DirectAdmin server                  │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  [🚀 Icon]  HestiaCP                             [Beta]     │
│  Import accounts from a HestiaCP server                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Interactions:**
- Click card to select
- Selected card shows checkmark and highlight border
- "Beta" badges show orange color
- "Ready" badges show green color
- Cards are responsive: 2 columns on tablet, 1 column on mobile

#### Step 2: Connection

**For Live Server (WHM, cPanel, DirectAdmin, HestiaCP):**

```
Connect to [Platform Name]
Enter the server details to discover accounts for migration

Server Host *          Port
[cpanel.example.com]   [2087 ▼]
                       (WHM default)

Authentication Method
[ Password ]  [ API Token ]

Username *
[root                    ]

Password *
[••••••••••••••••        ]

☑ Use SSL/TLS connection (recommended)

[The wizard will connect to your server to list available accounts]
```

**For File Upload (cpmove):**

```
Upload cpmove file
Upload the .tar.gz file exported from cPanel

┌─────────────────────────────────────────────────────────────┐
│                                                             │
│              [📤 Cloud Upload Icon]                         │
│                                                             │
│              Click or drag file to upload                   │
│                                                             │
│              Supported: .tar, .tar.gz                       │
│                                                             │
└─────────────────────────────────────────────────────────────┘

Uploaded: cpmove-laundryshco.tar.gz (752 MB) ✓
```

#### Step 3: Select Items

**For WHM / DirectAdmin / HestiaCP (Account Selection):**

```
Select Accounts
Choose which accounts to migrate from cpanel.example.com

Fetching accounts from server... [Spinner]

☑ account1  example1.com  1.2 GB
☑ account2  example2.com  856 MB
☐ account3  example3.com  2.1 GB

Selected: 2 accounts (2.06 GB total)
```

**For cPanel / cpmove (Item Selection):**

```
Select Items to Import
Choose what content to migrate

┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│     📁      │  │     🗄️      │  │     📧      │
│ Home        │  │ Databases   │  │ Email       │
│ Directory   │  │             │  │ Accounts    │
│ [Checked ✓] │  │ [Checked ✓] │  │ [Checked ✓] │
└─────────────┘  └─────────────┘  └─────────────┘

┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│     🌐      │  │     🔒      │  │     ⏰      │
│ DNS Zones   │  │ SSL         │  │ Cron Jobs   │
│ [Checked ✓] │  │ [Unchecked] │  │ [Unchecked] │
└─────────────┘  └─────────────┘  └─────────────┘
```

**Item Cards:**
- Icon, title, description
- Click to toggle selection
- Selected state: green border, checkmark
- Show what will be imported

#### Step 4: Review & Confirm

```
Ready to Start Migration
Review the details below and click Start Migration

┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  Source Platform                                            │
│  🏢 WHM (Full Server)                                       │
│                                                             │
│  Server                                                     │
│  root@192.168.100.168:2087                                  │
│                                                             │
│  Accounts to Migrate                                        │
│  2 accounts selected (2.06 GB total)                        │
│                                                             │
│  Items to Import                                            │
│  📁 Home  🗄️ Databases  📧 Email  🌐 DNS                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘

[ ⚠️ This will create new users and import all selected data. 
   Existing users with the same username will be skipped. ]

[       Start Migration       ]
```

### 4.3 Migration Detail Page

```
[Icon] Migration: laundryshco                          [Running]
From: root@192.168.100.168 (cPanel)
To: laundryshco
Started: 2 hours ago  •  Size: 752 MB

Progress Timeline:

Stage 1: Analyze        Stage 2: Fix Permissions   Stage 3: Restore
[✓ Done]               [✓ Done]                  [░░░ In Progress]
2 min                  30 sec                    65% complete

Stage Cards:

┌─────────────────────────────────────────────────────────────┐
│ 📁 Home Directory                                           │
│ [████████████████████░░░░░░░░░░] 65%                        │
│ 489 MB / 752 MB  •  Files, images, content                 │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ 🗄️ Databases                                                │
│ [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░] 0%                        │
│ ⏳ Queued                                                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ 📧 Email Accounts                                           │
│ [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░] 0%                        │
│ ⏳ Queued                                                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ 🌐 DNS Zones                                                │
│ [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░] 0%                        │
│ ⏳ Queued                                                   │
└─────────────────────────────────────────────────────────────┘

[Cancel Migration]
```

---

## 5. Component Architecture

### 5.1 New Components to Create

```
src/shells/admin/migrations/
├── AdminMigrationsPage.tsx           # REDESIGNED - List view
├── AdminMigrationDetailPage.tsx      # REDESIGNED - Detail view
├── CreateMigrationWizard.tsx         # NEW - 4-step wizard
├── components/
│   ├── MigrationJobCard.tsx          # NEW - Job card for list
│   ├── MigrationProgressCard.tsx     # NEW - Stage progress card
│   ├── StatsBar.tsx                  # NEW - Summary stats
│   ├── EmptyState.tsx                # NEW - Empty state illustration
│   └── SourceTypeCard.tsx            # NEW - Source selection card
├── steps/
│   ├── SourceTypeStep.tsx            # NEW - Step 1
│   ├── CredentialsStep.tsx           # NEW - Step 2
│   ├── AccountSelectionStep.tsx      # NEW - Step 3
│   └── SummaryStep.tsx               # NEW - Step 4
└── config/
    └── sourceTypes.ts                # NEW - Source type config
```

### 5.2 Component Responsibilities

**AdminMigrationsPage:**
- Display stats bar
- Handle filters
- Show migration cards or empty state
- Open wizard on "New Migration" click
- Poll for updates every 30 seconds

**CreateMigrationWizard:**
- Manage step state
- Collect form data across steps
- Validate each step
- Submit to API on final step
- Show loading/success states

**MigrationJobCard:**
- Display job info (source, target, status)
- Show progress bar
- Display action buttons (View, Cancel, Destroy)
- Handle responsive layout

**MigrationProgressCard:**
- Show stage icon, name, status
- Display progress bar
- Show bytes processed
- Handle error display

---

## 6. API Requirements

### 6.1 Existing Endpoints (No Changes)
```
GET    /admin/migrations              # List jobs
POST   /admin/migrations              # Create job
GET    /admin/migrations/:id          # Get job details
DELETE /admin/migrations/:id          # Cancel job
POST   /admin/migrations/:id/destroy  # Destroy job
POST   /admin/migrations/:id/secrets  # Upload credentials
POST   /admin/migrations/:id/pull-source  # Pull from source
POST   /admin/migrations/:id/import   # Run import
POST   /admin/migrations/:id/tarball  # Upload file
```

### 6.2 New Endpoints Needed
```
GET /admin/migrations/discover-accounts
  Request: { host, username, password, source_type }
  Response: { accounts: [{ username, domain, size, email }] }
  
  Purpose: List available accounts from source server
  
GET /admin/migrations/:id/progress
  Request: - 
  Response: { stages: [{ name, progress, bytes_processed, total_bytes, state }] }
  
  Purpose: Get detailed per-stage progress
```

### 6.3 Data Model

```typescript
// Migration Job
interface MigrationJob {
  id: string;
  source_type: "whm" | "cpanel" | "cpmove" | "directadmin" | "hestiacp";
  source_host: string;
  source_user: string;
  target_user: string | null;
  target_email: string | null;
  state: "pending" | "analyzing" | "fix_perms" | "validating" | "restoring" | "done" | "failed" | "cancelled";
  progress: number;          // Overall percentage
  stages: MigrationStage[];
  size_total: number;        // Total bytes
  size_processed: number;    // Processed bytes
  started_at: string;
  ended_at: string | null;
  created_at: string;
  items: string[];           // Selected items to import
  accounts: string[];        // Selected accounts (for WHM)
}

// Migration Stage
interface MigrationStage {
  id: string;
  job_id: string;
  stage_name: "analyze" | "fix_perms" | "validate" | "restore";
  state: "pending" | "running" | "done" | "failed";
  progress: number;
  bytes_processed: number;
  total_bytes: number;
  started_at: string | null;
  ended_at: string | null;
  last_error: string | null;
}
```

---

## 7. Responsive Design

### 7.1 Breakpoints
- **Mobile**: < 768px
- **Tablet**: 768px - 1024px
- **Desktop**: > 1024px

### 7.2 Mobile Adaptations

**Migration List:**
- Stats bar: Stack vertically (2x2 grid)
- Filter bar: Full-width search, stacked filters
- Cards: Full width, stacked layout
- Hide some columns, show in expanded view

**Wizard:**
- Steps: Show only current step number + title (hide descriptions)
- Source cards: 1 column
- Item grid: 1-2 columns
- Forms: Full-width inputs
- Summary: Stack sections vertically

**Detail Page:**
- Timeline: Vertical instead of horizontal
- Stage cards: Full width, stacked
- Log output: Collapsible

### 7.3 Tablet Adaptations

**Migration List:**
- Stats bar: Horizontal row
- Cards: 2-column grid
- Filters: Inline

**Wizard:**
- Source cards: 2 columns
- Item grid: 3 columns

---

## 8. State Machine

### 8.1 Job Lifecycle

```
[Create]
    │
    ▼
[PENDING] ──cancel──► [CANCELLED]
    │
    ▼
[ANALYZING] ──fail──► [FAILED]
    │
    ▼
[FIX_PERMS] ──fail──► [FAILED]
    │
    ▼
[VALIDATING] ──fail──► [FAILED]
    │
    ▼
[RESTORING] ──fail──► [FAILED]
    │
    ▼
[DONE]
    │
    └── destroy ──► [DESTROYED]

[FAILED]
    │
    ├── retry ──► [PENDING]
    │
    └── destroy ──► [DESTROYED]
```

### 8.2 User Actions by State

| State | Actions Available |
|-------|-------------------|
| Pending | Cancel |
| Analyzing | Cancel |
| Fix Perms | Cancel |
| Validating | Cancel |
| Restoring | Cancel |
| Done | Destroy, View |
| Failed | Retry, Destroy, View |
| Cancelled | Destroy, View |

---

## 9. Error Handling

### 9.1 Connection Errors (Step 2)
- **Invalid credentials**: Show error message, highlight fields
- **Connection timeout**: Retry button, suggest checking firewall
- **SSL error**: Toggle SSL off option
- **Server not found**: Validate hostname format

### 9.2 Account Discovery Errors (Step 3)
- **No accounts found**: Show message, suggest checking credentials
- **Permission denied**: Explain required WHM permissions
- **API rate limit**: Show countdown, auto-retry

### 9.3 Migration Errors
- **Stage failure**: Show in progress card with error message
- **Disk full**: Pause migration, show alert
- **User already exists**: Show conflict resolution options

---

## 10. Performance Considerations

### 10.1 Polling Strategy
- **List page**: Poll every 30 seconds
- **Detail page**: Poll every 10 seconds
- **Stop polling** when job reaches terminal state (done/failed/cancelled)
- **Backoff strategy**: Increase interval if no changes

### 10.2 Loading States
- **Account discovery**: Show skeleton screens
- **File upload**: Show progress bar
- **Migration start**: Show spinner on button

### 10.3 Optimization
- **Virtual scrolling** for large account lists (>100)
- **Lazy load** detail page data
- **Cache** account discovery results

---

## 11. Testing Requirements

### 11.1 E2E Tests
1. **Create migration flow** - All source types
2. **Cancel migration** - Verify cleanup
3. **View migration progress** - Check polling
4. **Filter migrations** - By status and source
5. **Mobile responsive** - All steps on 390px

### 11.2 Unit Tests
- Wizard step validation
- Progress calculation
- State transitions
- Error handling

---

## 12. Implementation Phases

### Phase 1: Foundation (Week 1)
- Create new components structure
- Implement CreateMigrationWizard shell
- Add SourceTypeStep and CredentialsStep
- Update routing

### Phase 2: Core Features (Week 2)
- Implement AccountSelectionStep and SummaryStep
- Create MigrationJobCard component
- Redesign AdminMigrationsPage
- Add stats bar and filters

### Phase 3: Detail View (Week 3)
- Redesign AdminMigrationDetailPage
- Create MigrationProgressCard
- Add timeline visualization
- Implement stage breakdown

### Phase 4: Polish (Week 4)
- Responsive design fixes
- Loading states and animations
- Error handling
- E2E tests

---

## 13. Open Questions

1. **Should we support bulk migration?** (Migrate multiple accounts at once)
2. **Should we show migration logs in real-time?** (Tail log file)
3. **Do we need email notifications?** (Migration complete/failed)
4. **Should we support scheduling?** (Start migration at off-peak hours)
5. **Do we need migration templates?** (Save common configurations)

---

## 14. Appendix

### A. Color Scheme
- **Primary**: #1890ff (Ant Design blue)
- **Success**: #52c41a (Green)
- **Warning**: #faad14 (Orange)
- **Error**: #f5222d (Red)
- **Info**: #1890ff (Blue)

### B. Typography
- **Page Title**: 24px, bold
- **Card Title**: 16px, medium
- **Body**: 14px, regular
- **Caption**: 12px, secondary color

### C. Spacing
- **Card padding**: 20px
- **Section gap**: 24px
- **Element gap**: 12px
- **Border radius**: 8px (cards), 12px (modals)

### D. Icons
- WHM: DatabaseOutlined
- cPanel: UserOutlined
- cpmove: FileZipOutlined
- DirectAdmin: CloudUploadOutlined
- HestiaCP: RocketOutlined
- Home: FolderOutlined
- Database: DatabaseOutlined
- Email: MailOutlined
- DNS: GlobalOutlined
- SSL: LockOutlined
- Cron: ClockCircleOutlined

---

**Document Owner:** Product Team  
**Stakeholders:** Engineering, Design, QA  
**Last Updated:** 2026-05-12

---

*This document serves as the master plan for the migration GUI redesign. All implementation should reference this document for UX decisions, component structure, and API requirements.*
