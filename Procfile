core: LOGOS_DIR=./data LOGOS_SOCK=/tmp/logos.sock LOGOS_MOD_SOCK=/tmp/logos-mod.sock LOGOS_CB_SOCK=/tmp/logos-cb.sock ./core/logos
mod-time: sleep 1 && LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-time/mod-time
mod-http: sleep 1 && LOGOS_CB_SOCK=/tmp/logos-cb.sock LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-http-server/mod-http-server
mod-sqlite: sleep 1 && LOGOS_CB_SOCK=/tmp/logos-cb.sock LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-sqlite/mod-sqlite
mod-mcp-server: sleep 1 && LOGOS_CB_SOCK=/tmp/logos-cb.sock LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-mcp-server/mod-mcp-server
mod-fs: sleep 1 && LOGOS_PROJECT_ROOT=. LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-fs/mod-fs
mod-git: sleep 1 && LOGOS_PROJECT_ROOT=. LOGOS_MOD_SOCK=/tmp/logos-mod.sock ./mod-git/mod-git
