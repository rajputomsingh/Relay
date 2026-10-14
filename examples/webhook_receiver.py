from http.server import BaseHTTPRequestHandler, HTTPServer

class WebhookHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)

        print("\n--- WEBHOOK RECEIVED ---")
        print("Path:", self.path)
        print("Idempotency-Key:", self.headers.get("Idempotency-Key"))
        print("Payload:", body.decode("utf-8"))

        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"received":true}')

    def log_message(self, format, *args):
        print(format % args)

HTTPServer(("127.0.0.1", 9090), WebhookHandler).serve_forever()
