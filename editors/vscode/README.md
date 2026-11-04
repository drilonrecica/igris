# Igris for VS Code

Watch and steer an [igris](https://github.com/drilonrecica/igris) run from the editor: the plan's phases and tasks,
what needs you, and what each task changed. It is a client of `igris serve`; the engine stays in the `igris` binary.

This is the scaffold (v1.9 work in progress): it checks at activation that `igris` 1.8.0 or newer is in `PATH`.

Needs `igris` 1.8.0 or newer in `PATH` (SPEC §15.9). The extension runs where the workspace's files are
(`extensionKind: ["workspace"]`), so it works under Remote-WSL and Remote-SSH.
