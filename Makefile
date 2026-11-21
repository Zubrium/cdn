ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

.PHONY: generate-profiles build build-full build-router build-dev-fixtures test test-router test-dev-fixtures coverage vet lint lint-core-boundary

# Root entry points are thin wrappers. Module boundaries and the single
# composition root remain defined in main/Makefile; no module code is linked
# or imported by this file.
generate-profiles build build-full build-router build-dev-fixtures test test-router test-dev-fixtures coverage vet lint lint-core-boundary:
	$(MAKE) -C "$(ROOT)/main" $@
