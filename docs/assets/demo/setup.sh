# Sourced by demo.tape: a throwaway Feat with one toy project, all under $D.
# Run demo.tape from the repository root after `make build`.
# Claude asks once to trust $D/src/shop; accept it, and later takes inherit it.

D=/tmp/feat-demo
export XDG_CONFIG_HOME=$D/config XDG_DATA_HOME=$D/state FEAT_RUNTIME_DIR=$D/run
export EDITOR=vi PATH="$PWD/bin:$PATH"
# The agent's endpoint may answer without a newline; hide zsh's % marker for it.
PROMPT_EOL_MARK=

if [ -d $D ]; then
	for p in $(docker compose ls -aq --filter name=feat-shop-); do
		docker compose -p "$p" down -v >/dev/null 2>&1
	done
	feat daemon stop >/dev/null 2>&1
	command tmux -S $D/run/tmux.sock kill-server 2>/dev/null
	rm -rf $D
fi

mkdir -p $D/src $D/config/feat/projects
git init -q --bare $D/src/origin.git
git init -q -b main $D/src/shop
mkdir $D/src/shop/api
printf '# shop\nA tiny storefront API.\n' >$D/src/shop/README.md
cat >$D/src/shop/api/server.py <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer

PRODUCTS = [{"id": 1, "name": "Mug", "price": 12}]


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(str(PRODUCTS).encode())


HTTPServer(("", 8000), Handler).serve_forever()
EOF
cat >$D/src/shop/compose.yaml <<'EOF'
services:
  api:
    image: python:3.12-alpine
    working_dir: /app
    command: python -u api/server.py
    volumes:
      - .:/app
    ports:
      - "8000:8000"
EOF
git -C $D/src/shop add -A
git -C $D/src/shop commit -qm init
git -C $D/src/shop remote add origin $D/src/origin.git
git -C $D/src/shop push -q origin main

cat >$D/config/feat/projects/shop.yaml <<EOF
version: 1
project:
  id: shop
  primary_repository: shop
repositories:
  shop:
    host_path: $D/src/shop
    default_access: read_write
    runtime:
      compose_files: [compose.yaml]
      container_path: /app
      services: [api]
      reachable: [api]
agent:
  execution:
    mode: host
runtime:
  provider: compose
EOF

docker pull -q python:3.12-alpine >/dev/null
feat daemon start >/dev/null
feat project add shop >/dev/null

clear
