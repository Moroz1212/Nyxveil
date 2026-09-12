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

async function waitForLatestTarget(page, targetVersion) {
  const latestRe = new RegExp('Latest stable:\\s*' + targetVersion.replace(/\./g, '\\.'));
  const checkBtn = page.getByRole('button', { name: 'Проверить обновления' });
  for (let i = 0; i < 60; i++) {
    const body = ((await page.textContent('body').catch(() => '')) || '');
    if (latestRe.test(body)) {
      console.log('CP_BUTTON_BROWSER_LATEST_VISIBLE=' + targetVersion);
      return;
    }
    if (i % 5 === 0) {
      console.error(
        'CP_BUTTON_BROWSER_WAIT_LATEST i=' +
          i +
          ' snippet=' +
          body.replace(/\s+/g, ' ').slice(0, 280)
      );
    }
    try {
      if (await checkBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
        const busy = await checkBtn.isDisabled().catch(() => true);
        if (!busy) {
          await checkBtn.click({ timeout: 10000 }).catch(() => {});
          await page.waitForTimeout(3000);
        }
      }
    } catch (_) {}
    await page.waitForTimeout(5000);
  }
  await dumpDiag(page, 'latest_target_not_visible');
  throw new Error('Latest stable did not become ' + targetVersion + ' (GitHub discovery failed)');
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
    await page.locator('button[type=submit]').click({ noWaitAfter: true });

    // Wait for MFA enrollment / 2FA / error without racing locator.count during navigation.
    try {
      await Promise.race([
        page.waitForURL((u) => String(u).includes('error=1'), { timeout: 120000 }),
        page.waitForURL((u) => String(u).includes('/account/mfa/setup'), { timeout: 120000 }),
        page.waitForURL((u) => String(u).includes('/account/login-2fa'), { timeout: 120000 }),
        page.getByRole('heading', { name: 'Настройка MFA' }).waitFor({
          state: 'visible',
          timeout: 120000,
        }),
        page.locator('input[name=code]').first().waitFor({ state: 'visible', timeout: 120000 }),
      ]);
    } catch (e) {
      await dumpDiag(page, 'post_login_wait');
      throw e;
    }
    if (page.url().includes('error=1')) {
      await dumpDiag(page, 'login_error');
      throw new Error('login rejected (error=1)');
    }

    // Prefer MFA setup when that page is active (first SuperAdmin).
    const onSetup =
      page.url().includes('/account/mfa/setup') ||
      (await page.getByRole('heading', { name: 'Настройка MFA' }).isVisible().catch(() => false));
    if (onSetup) {
      await page.locator('details summary').first().click({ timeout: 30000 }).catch(() => {});
      const keyInput = page.locator('input.mono[readonly]').first();
      await keyInput.waitFor({ state: 'visible', timeout: 60000 });
      const shared = await keyInput.inputValue();
      const uri = await page.locator('textarea.mono').inputValue().catch(() => '');
      totpSecret =
        extractSecret(shared + ' ' + uri, uri) || shared.replace(/\s+/g, '').toUpperCase();
      if (!totpSecret) throw new Error('could not extract MFA shared key');
      if (process.env.CP_TOTP_OUT) fs.writeFileSync(process.env.CP_TOTP_OUT, totpSecret);
      await page.locator('input[name=code]').first().waitFor({ state: 'visible', timeout: 60000 });
      await page.fill('input[name=code]', totp(totpSecret));
      await page.getByRole('button', { name: 'Включить MFA' }).click({ noWaitAfter: true });
      const cont = page.getByRole('link', { name: 'Продолжить' });
      await cont.waitFor({ state: 'visible', timeout: 60000 });
      await cont.click({ noWaitAfter: true });
    } else {
      await page.locator('input[name=code]').first().waitFor({ state: 'visible', timeout: 60000 });
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
    // Wait for Blazor InteractiveServer status panel (not just static chrome).
    await page.getByRole('heading', { name: 'Версия' }).waitFor({
      state: 'visible',
      timeout: 120000,
    });

    const checkBtn = page.getByRole('button', { name: 'Проверить обновления' });
    await checkBtn.waitFor({ state: 'visible', timeout: 60000 });
    // Poll GitHub discovery via UI refresh until Latest stable matches target.
    // GHA runners share unauthenticated api.github.com quotas; retries absorb that.
    await waitForLatestTarget(page, targetVersion);

    // Published 1.3.8 lacks data-testid="control-plane-update"; click by Russian button text.
    // Prefer testid when present (newer builds), else "Обновить до <version>".
    const updateByTestId = page.getByTestId('control-plane-update');
    const updateByText = page.getByRole('button', {
      name: new RegExp('Обновить до\\s+' + targetVersion.replace(/\./g, '\\.')),
    });
    const updateAny = page.getByRole('button', { name: /Обновить до/ });
    let updateBtn = updateByTestId;
    try {
      await updateByTestId.waitFor({ state: 'visible', timeout: 5000 });
    } catch (_) {
      updateBtn = updateByText;
      try {
        await updateByText.waitFor({ state: 'visible', timeout: 90000 });
      } catch (e) {
        await dumpDiag(page, 'update_button_missing');
        const buttons = await page.locator('button').allTextContents().catch(() => []);
        console.error('CP_BUTTON_BROWSER_BUTTONS=' + JSON.stringify(buttons));
        // Fall back to any "Обновить до …" if target-specific text mismatched.
        updateBtn = updateAny;
        await updateAny.waitFor({ state: 'visible', timeout: 30000 }).catch(async () => {
          throw e;
        });
      }
    }
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

    // CRITICAL: do not navigate away until Blazor finishes StartUpdateAsync handoff kickoff.
    // Immediate goto previously aborted the circuit before download/handoff began, while
    // "Latest stable: 1.3.9" already matched a naive body.includes(targetVersion) check.
    try {
      await Promise.race([
        page.getByText('Обновление авторизовано').waitFor({ state: 'visible', timeout: 600000 }),
        page.getByText(/Загрузка/).waitFor({ state: 'visible', timeout: 600000 }),
        page.getByRole('heading', { name: /Транзакция/ }).waitFor({
          state: 'visible',
          timeout: 600000,
        }),
        page.getByText(/package_verification_failed|PreflightFailed|ConcurrentUpdate/i).waitFor({
          state: 'visible',
          timeout: 600000,
        }),
      ]);
    } catch (e) {
      await dumpDiag(page, 'post_confirm_no_progress');
      throw e;
    }
    const earlyBody = ((await page.textContent('body').catch(() => '')) || '').toLowerCase();
    if (
      earlyBody.includes('package_verification_failed') ||
      earlyBody.includes('preflightfailed') ||
      (earlyBody.includes('alert-error') && earlyBody.includes('fail'))
    ) {
      await dumpDiag(page, 'update_start_error');
      throw new Error('Control Plane update failed to start after confirm');
    }
    console.log('CP_BUTTON_BROWSER_UPDATE_STARTED=PASS');

    const installedRe = new RegExp('Installed:\\s*' + targetVersion.replace(/\./g, '\\.'));
    let ok = false;
    for (let i = 0; i < 150; i++) {
      try {
        // Prefer soft reload so we do not tear down an in-flight Blazor update circuit early.
        await page.reload({ waitUntil: 'domcontentloaded', timeout: 30000 }).catch(async () => {
          await page.goto(base + '/admin/control-plane', {
            waitUntil: 'domcontentloaded',
            timeout: 30000,
          });
        });
        const body = (await page.textContent('body')) || '';
        if (installedRe.test(body)) {
          ok = true;
          break;
        }
        if (i % 6 === 0) {
          console.error(
            'CP_BUTTON_BROWSER_WAIT_INSTALLED i=' +
              i +
              ' snippet=' +
              body.replace(/\s+/g, ' ').slice(0, 240)
          );
        }
      } catch (_) {}
      await page.waitForTimeout(5000);
    }
    if (!ok) {
      await dumpDiag(page, 'post_update_missing_installed_version');
      throw new Error(
        'browser did not observe Installed: ' + targetVersion + ' after button update'
      );
    }
    console.log('CP_BUTTON_BROWSER_OBSERVED_INSTALLED_' + targetVersion.replace(/\./g, '_') + '=PASS');
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
