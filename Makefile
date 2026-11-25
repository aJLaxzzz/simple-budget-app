.DEFAULT_GOAL := help
PYTHON ?= python3
INSTANCE ?= acceptance
DEVICE ?= mac-1
AUTH ?= manual
SUDO_MODE ?= legacy
SECRET ?= app
export ENV INSTANCE ARCHIVE DEVICE BACKUP_ID DECISION LOCAL_DATA NAME ALLOW_CORRUPT_LIVE KIND AUTH SECRET PKI_ARCHIVE SUDO_MODE OUTPUT

.PHONY: help release-linux server-install initialize repair-initialization deploy migrate-data status logs backup fetch-backup restore rollback recover tls-status client-list client-issue client-renew client-add client-revoke pki-backup pki-export cleanup secure-page test-ops legacy-export acceptance acceptance-download acceptance-tls acceptance-http
help:
	@echo 'Server Manager: make server (starts with acceptance); AUTH=manual|keychain; SUDO_MODE=legacy|session'
	@echo 'Keychain: keychain-build keychain-status keychain-register keychain-check keychain-delete; SECRET=app|client|pki'
	@echo 'Local: make release-linux | test-ops | test-ops-native'
	@echo 'Server (explicit ENV=production, default INSTANCE=acceptance):'
	@echo '  server-install initialize repair-initialization deploy migrate-data status logs backup fetch-backup restore rollback recover'
	@echo '  tls-status client-list client-add client-revoke pki-export cleanup secure-page acceptance-download acceptance-tls acceptance-http'
	@echo 'PKI on Mac only: client-issue DEVICE=name | client-renew DEVICE=old NAME=new | pki-backup'
	@echo 'See docs/SERVER-OPERATIONS.md. No network operation runs by default.'
release-linux server-install initialize repair-initialization deploy migrate-data status logs backup fetch-backup restore rollback recover tls-status client-list client-issue client-renew client-add client-revoke pki-backup pki-export cleanup secure-page acceptance acceptance-download acceptance-tls acceptance-http:
	@$(PYTHON) scripts/ops/mac.py $@
test-ops:
	@$(PYTHON) -m unittest discover -s tests/ops -v
legacy-export:
	@$(PYTHON) scripts/ops/legacy.py

.PHONY: test-ops-native
test-ops-native:
	@$(PYTHON) tests/ops/cli_integration.py

.PHONY: server keychain-build keychain-status keychain-register keychain-check keychain-delete
server:
	@$(PYTHON) scripts/ops/server_manager.py
keychain-build:
	@/bin/sh scripts/ops/build-keychain.sh
keychain-status keychain-register keychain-check keychain-delete:
	@$(PYTHON) scripts/ops/mac.py $@

.PHONY: test-server-ux-native
test-server-ux-native: keychain-build
	@$(PYTHON) tests/ops/server_ux_native.py
