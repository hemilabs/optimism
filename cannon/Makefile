DEPRECATED_TARGETS := cannon cannon64-impl cannon-embeds clean diff-hello-elf sanitize-program contract test cannon-stf-verify fuzz lint

include ../justfiles/deprecated.mk

# Pattern target for backwards compatibility with make diff-<vm>-cannon invocations.
# Translates make diff-<vm>-cannon to just diff-cannon <vm>.
.PHONY: diff-%-cannon
diff-%-cannon:
	env $(JUSTFLAGS) just diff-cannon $*
