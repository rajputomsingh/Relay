
from http.server import BaseHTTPRequestHandler, HTTPServer


class FailureHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)

        print(
            f"Received {self.path}: "
            f"{body.decode('utf-8', errors='replace')}"
        )

        self.send_response(500)
        self.end_headers()
        self.wfile.write(b"Intentional test failure")


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 9001), FailureHandler)

    print("Failure receiver listening on port 9001")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nStopping failure receiver")
    finally:
        server.server_close()
