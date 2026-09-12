#!/usr/bin/env node
/**
 * real-node-button-browser.mjs
 *
 * Playwright helper: login to lab Control Plane, complete MFA enrollment/login,
 * step-up, open node details, click data-testid=node-update, confirm preflight
 * ("Запустить обновление").
 *
 * Required env:
 *   CP_BASE, CP_EMAIL, CP_PASSWORD, CP_NODE_ID
 * Optional:
 *   CP_TOTP_SECRET, CP_TOTP_OUT, CP_CLICK_MARKER, CP_TARGET_VERSION
 */
import { chromium } from 'playwright';
import fs from 'fs';
import crypto from 'crypto';

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

async function expectEnabled(locator, label) {
  for (let i = 0; i < 90; i++) {
    if (await locator.isEnabled()) return;
    await new Promise((r) => setTimeout(r, 2000));
  }
  throw new Error(`${label} stayed disabled`);
}

async function fillTotpIfPresent(page, secret) {
  const code = page.locator('input[name=code]');
  if ((await code.count()) === 0) return false;
  if (!secret) throw new Error('TOTP required but CP_TOTP_SECRET empty');
  await code.first().fill(totp(secret));
  const confirm = page.getByRole('button', { name: /Подтвердить|Войти|Включить MFA/ });
  await confirm.first().click();
  return true;
}

(async () => {
  const base = process.env.CP_BASE;
  const email = process.env.CP_EMAIL;
  const password = process.env.CP_PASSWORD;
  const nodeId = process.env.CP_NODE_ID;
  let totpSecret = process.env.CP_TOTP_SECRET || '';
  const targetVersion = process.env.CP_TARGET_VERSION || '';
  if (!base || !email || !password || !nodeId) {
    throw new Error('CP_BASE, CP_EMAIL, CP_PASSWORD, CP_NODE_ID are required');
  }

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.setDefaultTimeout(180000);

  await page.goto(base + '/account/login');
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', password);
  await page.getByRole('button', { name: 'Войти' }).click();

  // Mandatory MFA enrollment for SuperAdmin.
  if (
    page.url().includes('/account/mfa/setup') ||
    (await page.getByRole('heading', { name: 'Настройка MFA' }).count())
  ) {
    await page.locator('details summary').first().click({ timeout: 30000 }).catch(() => {});
    const keyInput = page.locator('input.mono[readonly]').first();
    await keyInput.waitFor({ state: 'visible', timeout: 60000 });
    const shared = await keyInput.inputValue();
    const uri = await page.locator('textarea.mono').inputValue().catch(() => '');
    totpSecret = extractSecret(shared + ' ' + uri, uri) || shared.replace(/\s+/g, '').toUpperCase();
    if (!totpSecret) throw new Error('could not extract MFA shared key');
    if (process.env.CP_TOTP_OUT) fs.writeFileSync(process.env.CP_TOTP_OUT, totpSecret);
    await page.fill('input[name=code]', totp(totpSecret));
    await page.getByRole('button', { name: 'Включить MFA' }).click();
    const cont = page.getByRole('link', { name: 'Продолжить' });
    await cont.waitFor({ state: 'visible', timeout: 60000 });
    await cont.click();
  }

  await fillTotpIfPresent(page, totpSecret);

  await page.waitForURL(
    (u) =>
      !String(u).includes('/account/login') &&
      !String(u).includes('/account/mfa/setup'),
    { timeout: 120000 }
  );

  const nodeUrl = '/admin/nodes/' + nodeId;
  await page.goto(
    base + '/account/mfa/step-up?returnUrl=' + encodeURIComponent(nodeUrl)
  );
  await fillTotpIfPresent(page, totpSecret);

  await page.goto(base + nodeUrl);
  const updateBtn = page.getByTestId('node-update');
  await updateBtn.waitFor({ state: 'visible', timeout: 120000 });
  await expectEnabled(updateBtn, 'node-update');
  await page.waitForTimeout(1000);
  await updateBtn.click();

  const preflight = page.getByRole('heading', { name: 'Проверка перед обновлением' });
  await preflight.waitFor({ state: 'visible', timeout: 90000 });

  // If step-up expired inside the modal, follow MFA CTA then reopen.
  const mfaCta = page.getByRole('link', { name: 'Подтвердить MFA' });
  if (await mfaCta.count()) {
    await mfaCta.first().click();
    await fillTotpIfPresent(page, totpSecret);
    await page.goto(base + nodeUrl);
    await updateBtn.waitFor({ state: 'visible' });
    await expectEnabled(updateBtn, 'node-update(retry)');
    await updateBtn.click();
    await preflight.waitFor({ state: 'visible', timeout: 90000 });
  }

  if (targetVersion) {
    const body = await page.locator('.modal').innerText();
    if (!body.includes(targetVersion)) {
      console.warn(
        'NODE_BUTTON_WARN=preflight target text missing ' + targetVersion + ' body=' + body.slice(0, 400)
      );
    }
  }

  const confirm = page.getByRole('button', { name: 'Запустить обновление' });
  await confirm.waitFor({ state: 'visible', timeout: 60000 });
  await expectEnabled(confirm, 'Запустить обновление');
  await confirm.click();

  if (process.env.CP_CLICK_MARKER) {
    fs.writeFileSync(
      process.env.CP_CLICK_MARKER,
      'clicked=' + new Date().toISOString() + '\nnode_id=' + nodeId + '\n'
    );
  }

  // Soft wait: command row / queue message may appear; durable wait is owned by bash.
  for (let i = 0; i < 30; i++) {
    try {
      await page.goto(base + nodeUrl, { waitUntil: 'domcontentloaded', timeout: 20000 });
      const text = (await page.textContent('body')) || '';
      if (
        text.includes('Обновление поставлено в очередь') ||
        text.includes('updated_healthy') ||
        /Обновление|UpdateNodeLatest|в очереди|выполняется/i.test(text)
      ) {
        break;
      }
    } catch (_) {}
    await page.waitForTimeout(3000);
  }

  await browser.close();
  console.log('NODE_BUTTON_BROWSER_CLICK=PASS');
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
