#!/usr/bin/env python3
"""Tiny two-tier HTTP/1.1 test app for the OBI e2e test.

  app.py backend  <port>                 -> 200 "ok"
  app.py frontend <port> <backend_url>   -> calls backend, returns 200/502

Each role runs under its own executable name (argv[0] is a copied
interpreter, see run-e2e.sh) so OBI discovery can tell them apart.
"""
import sys
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Backend(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        body = b"ok\n"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


def make_frontend(backend_url):
    class Frontend(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self):
            try:
                with urllib.request.urlopen(backend_url + self.path, timeout=5) as r:
                    data = r.read()
                code = 200
            except Exception as e:  # noqa
                data, code = (str(e) + "\n").encode(), 502
            self.send_response(code)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *a):
            pass

    return Frontend


if __name__ == "__main__":
    role, port = sys.argv[1], int(sys.argv[2])
    handler = Backend if role == "backend" else make_frontend(sys.argv[3])
    ThreadingHTTPServer(("127.0.0.1", port), handler).serve_forever()
