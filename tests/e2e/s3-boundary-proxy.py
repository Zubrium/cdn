#!/usr/bin/env python3
"""Disposable S3 boundary proxy for the live Website/Router E2E harness.

The QA object store is MinIO, which deliberately rejects the S3 public access
block API (it is listed in MinIO's ``rejectedBucketAPIs``). Core validates that
its bucket is private before it starts any component. This proxy answers that
single, auditable check with an explicit all-blocked configuration and forwards
every other request unchanged to MinIO. The signed Host, path, query, body and
headers are preserved, so SigV4 verification by the real object store still
applies to actual data operations.

This is test infrastructure only. Every interception is logged; no credential
is ever logged or modified.
"""

import http.client
import http.server
import os
import socketserver
import sys
from urllib.parse import urlsplit

UPSTREAM = urlsplit(os.environ["E2E_S3_UPSTREAM"])
BUCKET = os.environ["E2E_S3_BUCKET"]
PORT = int(os.environ["E2E_S3_PROXY_PORT"])
LOG_PATH = os.environ.get("E2E_S3_PROXY_LOG", "")

PRIVATE_BLOCK = (
    '<?xml version="1.0" encoding="UTF-8"?>'
    '<PublicAccessBlockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">'
    '<BlockPublicAcls>true</BlockPublicAcls>'
    '<IgnorePublicAcls>true</IgnorePublicAcls>'
    '<BlockPublicPolicy>true</BlockPublicPolicy>'
    '<RestrictPublicBuckets>true</RestrictPublicBuckets>'
    '</PublicAccessBlockConfiguration>'
).encode("utf-8")

HOP_BY_HOP = {
    "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
    "te", "trailer", "transfer-encoding", "upgrade",
}


def audit(message):
    line = "s3-boundary-proxy: " + message + "\n"
    sys.stderr.write(line)
    sys.stderr.flush()
    if LOG_PATH:
        with open(LOG_PATH, "a", encoding="utf-8") as handle:
            handle.write(line)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        return

    def _serve(self):
        self.close_connection = True
        length = int(self.headers.get("Content-Length", "0") or "0")
        body = self.rfile.read(length) if length > 0 else b""
        path = self.path
        query = path.split("?", 1)[1] if "?" in path else ""
        parameters = [part.split("=", 1)[0] for part in query.split("&")]
        is_private_check = (
            self.command == "GET"
            and path.startswith("/" + BUCKET + "?")
            and "publicAccessBlock" in parameters
        )

        if is_private_check:
            audit("private boundary check answered for bucket %s" % BUCKET)
            self.send_response(200)
            self.send_header("Content-Type", "application/xml")
            self.send_header("Content-Length", str(len(PRIVATE_BLOCK)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(PRIVATE_BLOCK)
            return

        connection = http.client.HTTPConnection(
            UPSTREAM.hostname, UPSTREAM.port or 80, timeout=120
        )
        try:
            headers = {
                key: value
                for key, value in self.headers.items()
                if key.lower() not in HOP_BY_HOP
            }
            connection.request(self.command, path, body=body, headers=headers)
            response = connection.getresponse()
            payload = response.read()
            audit("%s %s -> %d" % (self.command, path.split("?", 1)[0], response.status))
            self.send_response(response.status, response.reason)
            for key, value in response.getheaders():
                if key.lower() not in HOP_BY_HOP and key.lower() != "content-length":
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(payload)
        except Exception as error:
            audit("upstream failure for %s: %s" % (path.split("?", 1)[0], type(error).__name__))
            if not self.wfile.closed:
                try:
                    self.send_error(502, "bad gateway")
                except Exception:
                    pass
        finally:
            connection.close()

    do_GET = _serve
    do_PUT = _serve
    do_POST = _serve
    do_DELETE = _serve
    do_HEAD = _serve
    do_PATCH = _serve


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    if UPSTREAM.scheme != "http" or not UPSTREAM.hostname:
        raise SystemExit("E2E_S3_UPSTREAM must be an http URL")
    server = Server(("127.0.0.1", PORT), Handler)
    audit("listening on 127.0.0.1:%d, upstream %s" % (PORT, UPSTREAM.netloc))
    server.serve_forever()


if __name__ == "__main__":
    main()
