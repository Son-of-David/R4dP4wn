#!/bin/bash

# has to be run from 'build_support' subfolder
cd ..
echo "compiling P4wnP1_cli and P4wnP1_service ..."
ARCH="${P4WNP1_GOARCH:-arm64}"
env GOOS=linux GOARCH="$ARCH" go build -o build/P4wnP1_service ./cmd/P4wnP1_service
env GOOS=linux GOARCH="$ARCH" go build -o build/P4wnP1_cli ./cmd/P4wnP1_cli

echo "compiling web client to JavaScript ..."
cd web_client
gopherjs build -o ../build/webapp.js
python3 ../build_support/preserve_native_map.py ../build/webapp.js
cp ../build/webapp.js.map ../build/webmap.js
cp ../build/webapp.js ../dist/www/webapp.js
cp ../build/webapp.js.map ../dist/www/webapp.js.map
cp ../build/webmap.js ../dist/www/webmap.js

echo "...Results stored in ./build directory"
echo
echo "On P4wnP1 ALOA the compiled files have to be placed at the following"
echo "locations:"
echo
echo "    /usr/local/bin/P4wnP1_cli"
echo "    /usr/local/bin/P4wnP1_service"
echo "    /usr/local/P4wnP1/www/webapp.js"
echo "    /usr/local/P4wnP1/www/webapp.js.map"

