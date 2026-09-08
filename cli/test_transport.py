"""Credential confinement for account tokens and scoped connector credentials."""
import contextlib
import http.server
import threading
import unittest

import test_integrations as fixtures

G = fixtures.G


@contextlib.contextmanager
def server(handler):
    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    worker = threading.Thread(target=httpd.serve_forever, daemon=True)
    worker.start()
    try:
        yield "http://127.0.0.1:" + str(httpd.server_address[1])
    finally:
        httpd.shutdown()
        httpd.server_close()
        worker.join()


class TransportTests(unittest.TestCase):
    def test_redirects_and_foreign_absolute_downloads_never_receive_credentials(self):
        received = []

        class Target(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(self.headers.get("Authorization"))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"{}")

            def log_message(self, *_):
                pass

        with server(Target) as foreign:
            class Redirect(Target):
                def do_GET(self):
                    self.send_response(302)
                    self.send_header("Location", foreign + "/private")
                    self.end_headers()

                do_POST = do_GET

            with server(Redirect) as base:
                for token in ("fc_fixture", "fcc_fixture"):
                    client = G["Client"](base, token)
                    for method in ("GET", "POST"):
                        with self.assertRaises(G["APIError"]) as caught:
                            client.request(method, "/redirect", retries=0)
                        self.assertEqual(caught.exception.status, 302)
                        with self.assertRaises(G["APIError"]):
                            client.request_bytes(method, "/redirect")
                    with self.assertRaises(G["CLIError"]):
                        client.request_bytes("GET", foreign + "/file")
                self.assertEqual(received, [])
