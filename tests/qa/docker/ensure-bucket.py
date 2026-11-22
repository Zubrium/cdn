#!/usr/bin/env python3
"""Create the disposable MinIO bucket from the host, without an extra image.

The helper uses only the Python standard library and AWS Signature V4. It never
prints credentials; failures report only HTTP status and S3 error codes.
"""

import hashlib
import hmac
import http.client
import os
import re
import sys
import time
from datetime import datetime, timezone
from urllib.parse import urlsplit

BUCKET_RE = re.compile(r"^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$")
SERVICE = "s3"


def fail(message):
    print(f"ensure-bucket: {message}", file=sys.stderr)
    raise SystemExit(1)


def sha256_hex(value):
    return hashlib.sha256(value).hexdigest()


def hmac_bytes(key, message):
    return hmac.new(key, message.encode("ascii"), hashlib.sha256).digest()


def authorization(endpoint_host, bucket, access_key, secret_key, region):
    now = datetime.now(timezone.utc)
    amz_date = now.strftime("%Y%m%dT%H%M%SZ")
    date_stamp = now.strftime("%Y%m%d")
    payload_hash = sha256_hex(b"")
    canonical_uri = f"/{bucket}"
    signed_headers = "host;x-amz-content-sha256;x-amz-date"
    canonical_headers = (
        f"host:{endpoint_host}\n"
        f"x-amz-content-sha256:{payload_hash}\n"
        f"x-amz-date:{amz_date}\n"
    )
    canonical_request = (
        f"PUT\n{canonical_uri}\n\n{canonical_headers}\n{signed_headers}\n{payload_hash}"
    )
    scope = f"{date_stamp}/{region}/{SERVICE}/aws4_request"
    string_to_sign = "\n".join(
        [
            "AWS4-HMAC-SHA256",
            amz_date,
            scope,
            sha256_hex(canonical_request.encode("utf-8")),
        ]
    )
    key = hmac_bytes(("AWS4" + secret_key).encode("utf-8"), date_stamp)
    key = hmac_bytes(key, region)
    key = hmac_bytes(key, SERVICE)
    key = hmac_bytes(key, "aws4_request")
    signature = hmac.new(
        key, string_to_sign.encode("utf-8"), hashlib.sha256
    ).hexdigest()
    header = (
        f"AWS4-HMAC-SHA256 Credential={access_key}/{scope}, "
        f"SignedHeaders={signed_headers}, Signature={signature}"
    )
    return amz_date, payload_hash, header, canonical_uri


def error_code(body):
    match = re.search(rb"<Code>([^<]+)</Code>", body)
    return match.group(1).decode("utf-8", "replace") if match else ""


def main():
    endpoint = os.environ.get("WEBSITE_TEST_S3_ENDPOINT", "")
    bucket = os.environ.get("WEBSITE_TEST_S3_BUCKET", "")
    access_key = os.environ.get("WEBSITE_TEST_S3_ACCESS_KEY", "")
    secret_key = os.environ.get("WEBSITE_TEST_S3_SECRET_KEY", "")
    region = os.environ.get("WEBSITE_TEST_S3_REGION", "us-east-1")
    timeout_raw = os.environ.get("WEBSITE_QA_BUCKET_TIMEOUT", "60")

    if not all([endpoint, bucket, access_key, secret_key]):
        fail("set WEBSITE_TEST_S3_ENDPOINT, _BUCKET, _ACCESS_KEY and _SECRET_KEY")
    if not BUCKET_RE.match(bucket):
        fail("bucket name is not a lowercase disposable S3 bucket name")
    if not timeout_raw.isdigit() or int(timeout_raw) <= 0:
        fail("WEBSITE_QA_BUCKET_TIMEOUT must be a positive integer")

    parsed = urlsplit(endpoint)
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        fail("WEBSITE_TEST_S3_ENDPOINT must be an absolute http(s) URL")
    if parsed.path not in ("", "/"):
        fail("WEBSITE_TEST_S3_ENDPOINT must not contain a path prefix")
    host_header = parsed.netloc
    hostname = parsed.hostname
    if not hostname:
        fail("WEBSITE_TEST_S3_ENDPOINT has no host")
    try:
        port = parsed.port or (443 if parsed.scheme == "https" else 80)
    except ValueError:
        fail("WEBSITE_TEST_S3_ENDPOINT has an invalid port")

    deadline = time.monotonic() + int(timeout_raw)
    while True:
        amz_date, payload_hash, auth, canonical_uri = authorization(
            host_header, bucket, access_key, secret_key, region
        )
        connection = None
        try:
            connection_class = (
                http.client.HTTPSConnection
                if parsed.scheme == "https"
                else http.client.HTTPConnection
            )
            connection = connection_class(hostname, port, timeout=5)
            connection.request(
                "PUT",
                canonical_uri,
                body=b"",
                headers={
                    "Host": host_header,
                    "X-Amz-Content-Sha256": payload_hash,
                    "X-Amz-Date": amz_date,
                    "Authorization": auth,
                },
            )
            response = connection.getresponse()
            body = response.read()
            status = response.status
            code = error_code(body)
            if status == 200:
                print(f"ensure-bucket: bucket {bucket} ready")
                return
            if status == 409 and code in (
                "BucketAlreadyOwnedByYou",
                "BucketAlreadyExists",
            ):
                print(f"ensure-bucket: bucket {bucket} already exists for this run")
                return
            retryable = status in (500, 502, 503, 504)
            if not retryable:
                fail(f"bucket creation failed (HTTP {status}, S3 code {code or 'none'})")
        except (OSError, http.client.HTTPException) as exc:
            if time.monotonic() >= deadline:
                fail(f"MinIO endpoint did not accept bucket creation: {type(exc).__name__}")
            retryable = True
        finally:
            if connection is not None:
                connection.close()

        if not retryable or time.monotonic() >= deadline:
            fail("timed out waiting for MinIO bucket creation")
        time.sleep(1)


if __name__ == "__main__":
    main()
