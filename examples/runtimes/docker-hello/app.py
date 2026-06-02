import http.server
import socketserver


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'Docker app jalan di Jabali Panel\n'
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        return


with socketserver.TCPServer(('0.0.0.0', 8080), Handler) as httpd:
    print('Docker example listening on 8080')
    httpd.serve_forever()
