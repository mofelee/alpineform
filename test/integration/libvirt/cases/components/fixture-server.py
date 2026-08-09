#!/usr/bin/env python3

import argparse
import functools
import http.server
import threading
import urllib.parse


class SanitizedHandler(http.server.SimpleHTTPRequestHandler):
    log_path = ""
    log_lock = threading.Lock()

    def _without_query(self):
        return urllib.parse.urlsplit(self.path).path

    def _record_path(self, path):
        with self.log_lock:
            with open(self.log_path, "a", encoding="utf-8") as log_file:
                log_file.write(path + "\n")

    def _serve_sanitized(self, method):
        path = self._without_query()
        self._record_path(path)
        original = self.path
        self.path = path
        try:
            method()
        finally:
            self.path = original

    def do_GET(self):
        self._serve_sanitized(super().do_GET)

    def do_HEAD(self):
        self._serve_sanitized(super().do_HEAD)

    def log_message(self, _format, *_args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--bind", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--log", required=True)
    args = parser.parse_args()

    SanitizedHandler.log_path = args.log
    handler = functools.partial(SanitizedHandler, directory=args.directory)
    server = http.server.ThreadingHTTPServer((args.bind, args.port), handler)
    server.serve_forever()


if __name__ == "__main__":
    main()
