#!/usr/bin/env node
/**
 * Lab-only GitHub API TLS proxy for immutable Control Plane builds that lack
 * GITHUB_TOKEN support. Terminates TLS for api.github.com on 127.0.0.1:443,
 * injects Authorization: Bearer <token>, and forwards to real api.github.com.
 *
 * Requires: admin, hosts entry 127.0.0.1 api.github.com, lab CA/cert trusted,
 * env GH_TOKEN or GITHUB_TOKEN, CERT_PATH, KEY_PATH.
 *
 * Upstream DNS deliberately bypasses the hosts hijack (8.8.8.8 / 1.1.1.1).
 */
const https = require('https');
const fs = require('fs');
const { Resolver } = require('dns');

const token = (process.env.GH_TOKEN || process.env.GITHUB_TOKEN || '').trim();
if (!token) {
  console.error('lab-github-api-proxy: GH_TOKEN/GITHUB_TOKEN required');
  process.exit(1);
}
const certPath = process.env.CERT_PATH;
const keyPath = process.env.KEY_PATH;
if (!certPath || !keyPath || !fs.existsSync(certPath) || !fs.existsSync(keyPath)) {
  console.error('lab-github-api-proxy: CERT_PATH/KEY_PATH required');
  process.exit(1);
}

const upstreamHost = 'api.github.com';
const resolver = new Resolver();
resolver.setServers(['8.8.8.8', '1.1.1.1']);

function resolveUpstream(cb) {
  resolver.resolve4(upstreamHost, (err, addrs) => {
    if (err || !addrs || !addrs.length) {
      cb(err || new Error('no upstream A records'));
      return;
    }
    cb(null, addrs[0]);
  });
}

resolveUpstream((err, upstreamIp) => {
  if (err) {
    console.error('lab-github-api-proxy: upstream DNS failed', err.message || err);
    process.exit(1);
  }
  console.log('LAB_GITHUB_API_PROXY_UPSTREAM_IP=' + upstreamIp);

  const server = https.createServer(
    {
      cert: fs.readFileSync(certPath),
      key: fs.readFileSync(keyPath),
    },
    (req, res) => {
      const headers = { ...req.headers };
      headers.host = upstreamHost;
      headers.authorization = 'Bearer ' + token;
      headers['user-agent'] = headers['user-agent'] || 'Nyxveil-LabGitHubProxy';
      delete headers['content-length'];

      const opts = {
        host: upstreamIp,
        port: 443,
        path: req.url,
        method: req.method,
        headers,
        servername: upstreamHost,
      };
      const preq = https.request(opts, (pres) => {
        res.writeHead(pres.statusCode || 502, pres.headers);
        pres.pipe(res);
      });
      preq.on('error', (e) => {
        console.error('lab-github-api-proxy upstream error', e.message);
        if (!res.headersSent) res.writeHead(502);
        res.end('proxy upstream error');
      });
      req.pipe(preq);
    }
  );

  server.listen(443, '127.0.0.1', () => {
    console.log('LAB_GITHUB_API_PROXY=LISTEN 127.0.0.1:443 token=set');
  });
});
