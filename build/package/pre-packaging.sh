#!/bin/bash

ls "$BINARY_PATH"
ls -al
chmod 755 ./build/package/debian/DEBIAN/postinst
mkdir -p ./build/package/debian/usr/bin
cp "$BINARY_PATH"/pbs-d2d-backup ./build/package/debian/usr/bin/pbs-d2d-backup
