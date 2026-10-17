// A tiny app for Kayly's end-to-end test: one HTTP service, and the same file as worker.
const http = require('node:http');

if (process.argv[2] === 'worker') {
  console.log('worker started');
  setInterval(() => {}, 1 << 30);
} else {
  http.createServer((req, res) => {
    if (req.url === '/health') return res.end('ok');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({
      greeting: process.env.GREETING,
      origin: process.env.ORIGIN,
      hasAdmin: 'ADMIN_TOKEN' in process.env,
    }));
  }).listen(3000, () => console.log('listening on 3000'));
}
