#!/usr/bin/env node
/**
 * Playwright: login to installed Control Plane, MFA enroll/step-up,
 * click data-testid=control-plane-update, confirm, wait for UI 1.3.9.
 *
 * Env: CP_BASE, CP_EMAIL, CP_PASSWORD
 * Optional: CP_TOTP_SECRET, CP_TOTP_OUT, CP_CLICK_MARKER, CP_TARGET_VERSION
 */
const { chromium } = require('playwright');
const fs = require('fs');
const crypto = require('crypto');

function totp(secretB32) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  const cleaned = String(secretB32).replace(/[\s=]+/g, '').toUpperCase();
  for (const c of cleaned) {
    const val = alphabet.indexOf(c);
    if (val < 0) continue;
    bits += val.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    bytes.push(parseInt(bits.slice(i, i + 8), 2));
  }
  const key = Buffer.from(bytes);
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 1000 / 30)));
  const hmac = crypto.createHmac('sha1', key).update(counter).digest();
  const offset = hmac[hmac.length - 1] & 0xf;
  const code =
    ((hmac[offset] & 0x7f) << 24) |
    (hmac[offset + 1] << 16) |
    (hmac[offset + 2] << 8) |
    hmac[offset + 3];
  return String(code % 1000000).padStart(6, '0');
}

function extractSecret(pageText, otpUri) {
  if (otpUri) {
    const m = /[?&]secret=([A-Z2-7]+)/i.exec(otpUri);
    if (m) return m[1].toUpperCase();
  }
  const m2 = /([A-Z2-7]{16,})/.exec(String(pageText || '').replace(/\s+/g, ''));
  return m2 ? m2[1].toUpperCase() : '';
}

async function dumpDiag(page, label) {
  try {
    const url = page.url();
    const title = await page.title().catch(() => '');
    const body = ((await page.textContent('body').catch(() => '')) || '').slice(0, 800);
    console.error(`CP_BUTTON_BROWSER_DIAG=${label} url=${url} title=${title}`);
    console.error(body);
  } catch (e) {
    console.error(`CP_BUTTON_BROWSER_DIAG=${label} dump_failed=${e}`);
  }
}

async function expectEnabled(locator, label) {
  for (let i = 0; i < 90; i++) {
    if (await locator.isEnabled()) return;
    await new Promise((r) => setTimeout(r, 2000));
  }
  throw new Error(`${label} stayed disabled`);
}

(async () => {
  const base = process.env.CP_BASE;
  const email = process.env.CP_EMAIL;
  const password = process.env.CP_PASSWORD;
  const targetVersion = process.env.CP_TARGET_VERSION || '1.3.9';
  let totpSecret = process.env.CP_TOTP_SECRET || '';
  if (!base || !email || !password) {
    throw new Error('CP_BASE/CP_EMAIL/CP_PASSWORD required');
  }

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.setDefaultTimeout(60000);

  try {
    await page.goto(base + '/account/login', { waitUntil: 'domcontentloaded' });
    await page.locator('input[name=email]').waitFor({ state: 'visible', timeout: 60000 });
    await page.fill('input[name=email]', email);
    await page.fill('input[name=password]', password);
    // Form POST navigates into Blazor MFA pages; do not wait for full load on click.
    await page.locator('button[type=submit]').click({ noWaitAfter: true });

    // MFA enrollment (first SuperAdmin) or login-2fa.
    const codeInput = page.locator('input[name=code]');
    await codeInput.first().waitFor({ state: 'visible', timeout: 120000 });

    if (
      page.url().includes('/account/mfa/setup') ||
      (await page.getByRole('heading', { name: 'Настройка MFA' }).count())
    ) {
      await page.locator('details summary').first().click({ timeout: 30000 }).catch(() => {});
      const keyInput = page.locator('input.mono[readonly]').first();
      await keyInput.waitFor({ state: 'visible', timeout: 60000 });
      const shared = await keyInput.inputValue();
      const uri = await page.locator('textarea.mono').inputValue().catch(() => '');
      totpSecret =
        extractSecret(shared + ' ' + uri, uri) || shared.replace(/\s+/g, '').toUpperCase();
      if (!totpSecret) throw new Error('could not extract MFA shared key');
      if (process.env.CP_TOTP_OUT) fs.writeFileSync(process.env.CP_TOTP_OUT, totpSecret);
      await page.fill('input[name=code]', totp(totpSecret));
      await page.getByRole('button', { name: 'Включить MFA' }).click({ noWaitAfter: true });
      const cont = page.getByRole('link', { name: 'Продолжить' });
      await cont.waitFor({ state: 'visible', timeout: 60000 });
      await cont.click({ noWaitAfter: true });
    } else {
      if (!totpSecret) throw new Error('TOTP required for login-2fa');
      await page.fill('input[name=code]', totp(totpSecret));
      await page
        .getByRole('button', { name: /Подтвердить|Войти/ })
        .click({ noWaitAfter: true });
    }

    await page.waitForURL(
      (u) =>
        !String(u).includes('/account/login') &&
        !String(u).includes('/account/mfa/setup') &&
        !String(u).includes('/account/login-2fa'),
      { timeout: 120000 }
    );

    await page.goto(
      base + '/account/mfa/step-up?returnUrl=' + encodeURIComponent('/admin/control-plane'),
      { waitUntil: 'domcontentloaded' }
    );
    if ((await page.locator('input[name=code]').count()) > 0) {
      await page.fill('input[name=code]', totp(totpSecret));
      await page.getByRole('button', { name: 'Подтвердить' }).click({ noWaitAfter: true });
    }

    await page.goto(base + '/admin/control-plane', { waitUntil: 'domcontentloaded' });
    await page.getByRole('heading', { name: 'Control Plane' }).waitFor({
      state: 'visible',
      timeout: 60000,
    });

    const checkBtn = page.getByRole('button', { name: 'Проверить обновления' });
    await checkBtn.waitFor({ state: 'visible', timeout: 60000 });
    await checkBtn.click();
    // Allow discovery / cache refresh
    await page.waitForTimeout(8000);

    const updateBtn = page.getByTestId('control-plane-update');
    await updateBtn.waitFor({ state: 'visible', timeout: 120000 });
    await expectEnabled(updateBtn, 'control-plane-update');
    await updateBtn.click();

    const confirm = page.getByRole('button', { name: 'Подтвердить обновление' });
    await confirm.waitFor({ state: 'visible', timeout: 90000 });
    await expectEnabled(confirm, 'Подтвердить обновление');
    await confirm.click({ noWaitAfter: true });
    if (process.env.CP_CLICK_MARKER) {
      fs.writeFileSync(
        process.env.CP_CLICK_MARKER,
        'clicked=' + new Date().toISOString() + '\naction=control-plane-update\n'
      );
    }
    console.log('CP_BUTTON_BROWSER_CLICK=PASS');

    let ok = false;
    for (let i = 0; i < 120; i++) {
      try {
        await page.goto(base + '/admin/control-plane', {
          waitUntil: 'domcontentloaded',
          timeout: 20000,
        });
        const body = (await page.textContent('body')) || '';
        if (body.includes(targetVersion)) {
          ok = true;
          break;
        }
      } catch (_) {}
      await page.waitForTimeout(5000);
    }
    if (!ok) {
      await dumpDiag(page, 'post_update_missing_version');
      throw new Error('browser did not observe VERSION ' + targetVersion + ' after button update');
    }
    console.log('CP_BUTTON_BROWSER_OBSERVED_' + targetVersion.replace(/\./g, '_') + '=PASS');
  } catch (err) {
    await dumpDiag(page, 'failure');
    throw err;
  } finally {
    await browser.close();
  }
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
