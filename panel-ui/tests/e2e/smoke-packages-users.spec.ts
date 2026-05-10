import { test, expect } from '@playwright/test';

const BASE_URL = 'https://mx.jabali-panel.local:8443';
const ADMIN_EMAIL = 'admin@jabali.local';
const ADMIN_PASS = 'ONbCTkgmn5HGuhGEOuCpigvv';

// Test package configuration
const TEST_PACKAGE = {
  name: 'QA-Test-Package-' + Date.now(),
  disk_quota_mb: 100,
  cpu_quota_percent: 50,
  memory_limit_mb: 512,
  io_read_mbps: 10,
  io_write_mbps: 10,
  max_tasks: 50,
  bandwidth_quota_mb: 1000,
  max_domains: 2,
  max_email_accounts: 3,
  max_databases: 2,
  max_ftp_accounts: 1,
};

const TEST_USER = {
  email: 'qa-test-' + Date.now() + '@example.com',
  username: 'qatest' + Date.now(),
  password: 'TestPass123!',
};

// Store created resource IDs for cleanup
const state: { packageId?: string; userId?: string } = {};

async function loginAsAdmin(page: any) {
  await page.goto(BASE_URL + '/login');
  await page.waitForLoadState('networkidle');
  
  // Wait for Kratos login form to render
  await page.waitForSelector('input[type="text"], input[type="email"]', { timeout: 15000 });
  
  // Fill email and password using labels (Ant Design forms use labels, not name attrs)
  await page.getByLabel('Email').fill(ADMIN_EMAIL);
  await page.getByLabel('Password').fill(ADMIN_PASS);
  
  // Submit - the button text is "Sign in"
  await page.click('button:has-text("Sign in")');
  
  // Wait for admin dashboard
  await page.waitForURL(/.*jabali-admin.*/, { timeout: 15000 });
  await page.waitForLoadState('networkidle');
}

async function cleanup(page: any) {
  // Logout first to start fresh, then login as admin for cleanup
  try {
    await page.goto(BASE_URL + '/logout');
    await page.waitForTimeout(1000);
  } catch {
    // ignore
  }
  
  await loginAsAdmin(page);
  
  // Delete test user if created
  if (state.userId) {
    try {
      await page.goto(BASE_URL + '/jabali-admin/users');
      await page.waitForLoadState('networkidle');
      
      // Search for the user
      const searchInput = page.locator('input[placeholder*="Search"]').first();
      if (await searchInput.isVisible().catch(() => false)) {
        await searchInput.fill(TEST_USER.email);
        await page.waitForTimeout(1000);
        
        // Click delete if found
        const deleteBtn = page.locator('button:has-text("Delete")').first();
        if (await deleteBtn.isVisible().catch(() => false)) {
          await deleteBtn.click();
          // Confirm delete
          await page.click('button:has-text("OK"), button:has-text("Delete")');
          await page.waitForTimeout(1000);
        }
      }
    } catch {
      // ignore cleanup errors
    }
  }
  
  // Delete test package if created
  if (state.packageId) {
    try {
      await page.goto(BASE_URL + '/jabali-admin/packages');
      await page.waitForLoadState('networkidle');
      
      // Find and delete the package
      const pkgRow = page.locator(`text=${TEST_PACKAGE.name}`).first();
      if (await pkgRow.isVisible().catch(() => false)) {
        await pkgRow.click();
        await page.waitForLoadState('networkidle');
        
        // Look for delete action
        const deleteBtn = page.locator('button:has-text("Delete")').first();
        if (await deleteBtn.isVisible().catch(() => false)) {
          await deleteBtn.click();
          await page.click('button:has-text("OK"), button:has-text("Delete")');
          await page.waitForTimeout(1000);
        }
      }
    } catch {
      // ignore cleanup errors
    }
  }
}

async function fillNumberInput(page: any, label: string, value: string) {
  const input = page.getByLabel(label);
  // Check if enabled before filling
  const isEnabled = await input.isEnabled().catch(() => false);
  if (!isEnabled) {
    console.log(`Skipping disabled field: ${label}`);
    return;
  }
  // Clear and fill
  await input.click();
  await input.fill(value);
}

async function createPackage(page: any, pkg: typeof TEST_PACKAGE) {
  await page.goto(BASE_URL + '/jabali-admin/packages/create');
  await page.waitForLoadState('networkidle');
  
  // Fill package name
  await page.getByLabel('Name').fill(pkg.name);
  
  // Fill resource limits (skip disabled fields)
  await fillNumberInput(page, 'Disk Quota (MB)', pkg.disk_quota_mb.toString());
  await fillNumberInput(page, 'CPU Quota (%)', pkg.cpu_quota_percent.toString());
  await fillNumberInput(page, 'Memory Limit (MB)', pkg.memory_limit_mb.toString());
  await fillNumberInput(page, 'IO Read Bandwidth (MB/s)', pkg.io_read_mbps.toString());
  await fillNumberInput(page, 'IO Write Bandwidth (MB/s)', pkg.io_write_mbps.toString());
  await fillNumberInput(page, 'Max Tasks', pkg.max_tasks.toString());
  
  // Fill feature quotas
  await fillNumberInput(page, 'Bandwidth Quota (MB)', pkg.bandwidth_quota_mb.toString());
  await fillNumberInput(page, 'Max Domains', pkg.max_domains.toString());
  await fillNumberInput(page, 'Max Email Accounts', pkg.max_email_accounts.toString());
  await fillNumberInput(page, 'Max Databases', pkg.max_databases.toString());
  await fillNumberInput(page, 'Max FTP Accounts', pkg.max_ftp_accounts.toString());
  
  // Submit form
  await page.click('button:has-text("Save")');
  
  // Wait for redirect to packages list
  await page.waitForURL(/.*jabali-admin\/packages$/, { timeout: 10000 });
  
  // Verify package appears in list
  await expect(page.locator('text=' + pkg.name).first()).toBeVisible();
}

async function createUser(page: any, user: typeof TEST_USER, packageName: string) {
  await page.goto(BASE_URL + '/jabali-admin/users');
  await page.waitForLoadState('networkidle');
  
  // Click Create button to open drawer
  await page.click('button:has-text("Create")');
  
  // Wait for drawer to open - use title text instead of CSS class
  await page.waitForSelector('text=Create user', { timeout: 10000 });
  
  // Fill user form
  await page.getByLabel('Email').fill(user.email);
  await page.getByLabel('Username').fill(user.username);
  await page.getByLabel('Password').fill(user.password);
  
  // Select package from dropdown
  await page.getByPlaceholder('Select a package (optional)').click();
  await page.click(`.ant-select-item:has-text("${packageName}")`);
  
  // Submit - button text is "Create" in the drawer
  await page.click('button:has-text("Create")');
  
  // Wait for drawer to close
  await page.waitForSelector('text=Create user', { state: 'hidden', timeout: 10000 });
  
  // Verify user appears in list
  await expect(page.locator('text=' + user.email).first()).toBeVisible();
}

test.describe('Smoke Tests - Packages and Users', () => {
  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanup(page);
    await page.close();
  });

  test('Admin can login and access dashboard', async ({ page }) => {
    await loginAsAdmin(page);
    
    // Verify dashboard loaded - check URL and key dashboard elements
    await expect(page.locator('text=' + ADMIN_EMAIL).first()).toBeVisible();
    await expect(page.locator('text=Total Users').first()).toBeVisible();
    await expect(page.locator('text=Active Domains').first()).toBeVisible();
  });

  test('Create package with resource limits', async ({ page }) => {
    await loginAsAdmin(page);
    await createPackage(page, TEST_PACKAGE);
    state.packageId = 'created'; // Mark for cleanup
    
    // Verify package details by clicking on it
    await page.click('text=' + TEST_PACKAGE.name);
    await page.waitForLoadState('networkidle');
    
    // Check limits are displayed correctly (on edit page)
    await expect(page.locator('text=' + TEST_PACKAGE.disk_quota_mb)).toBeVisible();
    await expect(page.locator('text=' + TEST_PACKAGE.max_domains)).toBeVisible();
  });

  test('Create user and assign package', async ({ page }) => {
    await loginAsAdmin(page);
    await createUser(page, TEST_USER, TEST_PACKAGE.name);
    state.userId = 'created'; // Mark for cleanup
    
    // Verify user has correct package by clicking edit
    await page.locator('text=' + TEST_USER.email).first().click();
    await page.waitForSelector('.ant-drawer-content', { timeout: 10000 });
    
    // Check package is selected in the dropdown
    const selectValue = await page.locator('.ant-select-selection-item').textContent();
    expect(selectValue).toContain(TEST_PACKAGE.name);
  });

  test('Package list shows all created packages', async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(BASE_URL + '/jabali-admin/packages');
    await page.waitForLoadState('networkidle');
    
    // Verify our test package is visible
    await expect(page.locator('text=' + TEST_PACKAGE.name)).toBeVisible();
  });

  test('Package edit updates limits correctly', async ({ page }) => {
    await loginAsAdmin(page);
    
    // Navigate to first test package
    await page.goto(BASE_URL + '/jabali-admin/packages');
    await page.click('text=' + TEST_PACKAGE.name);
    await page.waitForLoadState('networkidle');
    
    // Click Edit (assumes there's an edit button/link)
    await page.click('button:has-text("Edit"), a:has-text("Edit")');
    await page.waitForLoadState('networkidle');
    
    // Update a limit
    const newQuota = TEST_PACKAGE.disk_quota_mb + 50;
    await page.getByLabel('Disk Quota (MB)').fill(newQuota.toString());
    
    // Save
    await page.click('button:has-text("Save")');
    
    // Verify update - wait for redirect back to list
    await page.waitForURL(/.*jabali-admin\/packages$/, { timeout: 10000 });
    await page.waitForLoadState('networkidle');
    
    // Navigate back to edit to verify
    await page.click('text=' + TEST_PACKAGE.name);
    await page.click('button:has-text("Edit"), a:has-text("Edit")');
    await page.waitForLoadState('networkidle');
    
    const inputValue = await page.getByLabel('Disk Quota (MB)').inputValue();
    expect(inputValue).toBe(newQuota.toString());
  });
});

test.describe('Bug Hunting - Edge Cases', () => {
  test('Package name validation - empty name', async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(BASE_URL + '/jabali-admin/packages/create');
    await page.waitForLoadState('networkidle');
    
    // Try to submit without name
    await page.click('button:has-text("Save")');
    
    // Should show validation error
    await expect(page.locator('text=required')).toBeVisible();
  });

  test('Package name validation - duplicate name', async ({ page }) => {
    await loginAsAdmin(page);
    
    // Create package first
    const dupPkg = { ...TEST_PACKAGE, name: 'Dup-Test-' + Date.now() };
    await createPackage(page, dupPkg);
    
    // Try to create another with same name
    await page.goto(BASE_URL + '/jabali-admin/packages/create');
    await page.waitForLoadState('networkidle');
    await page.getByLabel('Name').fill(dupPkg.name);
    await page.click('button:has-text("Save")');
    
    // Should show error - either validation or server error
    await expect(page.locator('text=already exists, text=already taken, text=conflict')).toBeVisible({ timeout: 5000 }).catch(() => {
      // Some systems show a toast instead
      return expect(page.locator('.ant-message-error, .ant-message-notice-content')).toBeVisible();
    });
  });

  test('Negative quota values are rejected', async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(BASE_URL + '/jabali-admin/packages/create');
    await page.waitForLoadState('networkidle');
    
    await page.getByLabel('Name').fill('Negative-Test-' + Date.now());
    await page.getByLabel('Disk Quota (MB)').fill('-100');
    await page.click('button:has-text("Save")');
    
    // Should show validation error for negative value
    const errorVisible = await page.locator('text=must be, text=cannot be, text=minimum').isVisible().catch(() => false);
    if (!errorVisible) {
      // InputNumber min=0 might prevent negative input entirely
      const value = await page.getByLabel('Disk Quota (MB)').inputValue();
      expect(value).not.toBe('-100');
    }
  });

  test('Zero quotas mean unlimited', async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(BASE_URL + '/jabali-admin/packages/create');
    await page.waitForLoadState('networkidle');
    
    const unlimitedPkg = {
      name: 'Unlimited-' + Date.now(),
      disk_quota_mb: 0,
      cpu_quota_percent: 0,
      memory_limit_mb: 0,
      io_read_mbps: 0,
      io_write_mbps: 0,
      max_tasks: 0,
      bandwidth_quota_mb: 0,
      max_domains: 0,
      max_email_accounts: 0,
      max_databases: 0,
      max_ftp_accounts: 0,
    };
    
    await page.getByLabel('Name').fill(unlimitedPkg.name);
    await page.getByLabel('Disk Quota (MB)').fill('0');
    await page.getByLabel('Max Domains').fill('0');
    
    // Fill other required fields
    await page.getByLabel('Bandwidth Quota (MB)').fill('0');
    await page.getByLabel('Max Email Accounts').fill('0');
    await page.getByLabel('Max Databases').fill('0');
    await page.getByLabel('Max FTP Accounts').fill('0');
    
    await page.click('button:has-text("Save")');
    
    // Should succeed - 0 means unlimited
    await page.waitForURL(/.*jabali-admin\/packages$/, { timeout: 10000 });
    await expect(page.locator('text=' + unlimitedPkg.name)).toBeVisible();
  });
});
