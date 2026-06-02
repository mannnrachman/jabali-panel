const http = require('http');

const port = Number(process.env.PORT || 3000);

http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
  res.end('Node.js app jalan di Jabali Panel\n');
}).listen(port, '127.0.0.1', () => {
  console.log(`Node.js example listening on ${port}`);
});
