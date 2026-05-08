#!/bin/sh
# Downloads the Linux x86_64 / cp310 wheels for Playwright and its
# dependencies into ./vendor/. The Docker build copies these wheels
# into the image and pip-installs them offline, so we never depend
# on PyPI being reachable from Fly's remote builder (which has been
# unreliable: connection-reset-by-peer 5/5 retries).
#
# Re-run after bumping the Playwright version in the Dockerfile.
set -eu

cd "$(dirname "$0")/.."
mkdir -p vendor
rm -f vendor/*.whl

PLATFORM_FLAGS="--platform manylinux1_x86_64 --platform manylinux_2_17_x86_64 --platform manylinux2014_x86_64 --abi cp310 --python-version 310 --only-binary=:all: --no-deps"

pip3 download $PLATFORM_FLAGS --dest vendor "playwright==1.49.1"
pip3 download $PLATFORM_FLAGS --dest vendor "greenlet==3.1.1"
pip3 download --no-deps --only-binary=:all: --dest vendor "pyee==12.0.0"
pip3 download --no-deps --only-binary=:all: --dest vendor "typing-extensions"

echo "vendor/ contents:"
ls -la vendor/
