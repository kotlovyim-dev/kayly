// A tiny app for Kayly's end-to-end test: one HTTP service, and the same file as worker.
const http = require('node:http');
const net = require('node:net');

// Resolves to true if host:port of a URL accepts a TCP connection.
const reachable = url => new Promise(ok => {
  if (!url) return ok(null);
  const { hostname, port } = new URL(url);
  const s = net.connect(Number(port), hostname, () => { s.end(); ok(true); }).on('error', () => ok(false));
});

if (process.argv[2] === 'worker') {
  console.log('worker started');
  setInterval(() => {}, 1 << 30);
} else {
  http.createServer(async (req, res) => {
    if (req.url === '/health') return res.end('ok');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({
      greeting: process.env.GREETING,
      origin: process.env.ORIGIN,
      hasAdmin: 'ADMIN_TOKEN' in process.env,
      postgres: await reachable(process.env.DATABASE_URL),
      redis: await reachable(process.env.REDIS_URL),
    }));
  }).listen(3000, () => console.log('listening on 3000'));
}
