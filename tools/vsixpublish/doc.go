// Package vsixpublish holds the tests of tools/vsix-publish.sh, the
// owner's step that publishes the editor extension (SPEC §15.9,
// decisions.md V19-P1, docs/threat-model.md R80): it publishes the very
// .vsix vsix-check verified, and the store tokens never reach the check's
// npm and build steps.
package vsixpublish
