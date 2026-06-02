import http.server
import os
import socketserver


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'Python app jalan di Jabali Panel\n'
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        return


port = int(os.environ.get('PORT', '3000'))
with socketserver.TCPServer(('127.0.0.1', port), Handler) as httpd:
    print(f'Python example listening on {port}')
    httpd.serve_forever()
