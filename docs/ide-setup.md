# IDE Setup Guide

This document covers configuring your IDE (particularly VSCode with Pylance) for development on the `smp` and `smp_bindings` packages.

## Overview

When `smp_bindings` is installed (via `pip install -e .` or `pip install .`), Python can import it normally from any project. However, VSCode's Pylance linter may fail to recognize the package even when the Python runtime does, resulting in false "unresolved import" errors.

This section explains the root cause and the fix.

## Root Cause

Pylance and other IDE language servers maintain a separate index of installed packages and source paths from the runtime. If:

1. The Python interpreter is set correctly, but
2. Pylance hasn't been told which environment paths to search,

then the IDE will fail to resolve imports even though `python -c "import smp_bindings"` works fine.

## Solution

### Step 1: Select the Correct Python Interpreter

VSCode must know which Python environment to use.

1. Open VSCode
2. Press `Cmd+Shift+P` (macOS) or `Ctrl+Shift+P` (Linux/Windows)
3. Search for and select **"Python: Select Interpreter"**
4. Choose your environment:
   - If using conda: e.g. `./miniconda3/envs/your-env-name/bin/python`
   - If using a virtualenv: e.g. `./venv/bin/python`
   - Typically shows with an environment badge like `(base)` or `(your-env)`

### Step 2: Install in Editable Mode (Recommended)

Editable installs ensure that the package's source path is correctly registered:

```bash
# Navigate to the project root
cd /path/to/social-media-models

# Activate your environment
conda activate your-env  # or 'source venv/bin/activate'

# Install in editable mode
pip install -e .
```

This creates a link in your Python environment's `site-packages` that points to the current repository, ensuring Pylance sees the source code directly.

### Step 3: Restart VSCode

Close and reopen VSCode completely. Pylance will re-index packages and should now resolve `smp_bindings` imports.

> **Tip**: If errors persist, open the **Output** panel (`View → Output`) and select **"Pylance"** from the dropdown. Check for diagnostic messages that might reveal missing paths or interpreter issues.

## Configuration Files

The project includes pre-configured files to support IDE tooling:

### `.vscode/settings.json`

```json
{
  "python-envs.defaultEnvManager": "ms-python.python:conda",
  "python-envs.defaultPackageManager": "ms-python.python:conda",
  "python.linting.enabled": true,
  "python.analysis.typeCheckingMode": "basic",
  "python.analysis.diagnosticMode": "openFilesOnly",
  "[python]": {
    "editor.defaultFormatter": "ms-python.python",
    "editor.formatOnSave": true
  }
}
```

This configures:

- **Environment manager**: Tells VS Code to use conda for environment detection
- **Type checking mode**: `"basic"` enables light type checking without full analysis
- **Diagnostic mode**: `"openFilesOnly"` checks only the currently open files for faster feedback

### `pyproject.toml` — `[tool.pyright]` section

```toml
[tool.pyright]
pythonVersion = "3.10"
typeCheckingMode = "basic"
include = ["smp_bindings"]
extraPaths = ["${workspaceFolder}"]
```

Tells Pylance (which uses pyright internally) to:

- Target Python 3.10+ for type checking
- Include the `smp_bindings` package in analysis
- Add the workspace root to the module search path

### `smp_bindings/py.typed`

An empty marker file in the package root indicating that the package includes type information. This signals to Pylance and other type checkers that type stubs or annotations are present.

## Troubleshooting

### "Unresolved import" errors for `smp_bindings`

1. **Check interpreter selection**: Ensure the correct environment is selected (see Step 1 above)
2. **Reinstall in editable mode**: Run `pip install -e .` again
3. **Restart Pylance**: Press `Cmd+Shift+P` and search "Pylance: Restart Pylance Server"
4. **Clear cache**: Delete `.vscode/.pylance` if it exists and restart

### Import works in terminal but fails in IDE

This is the classic symptom. Follow steps 1–3 above. Also verify:

```bash
# Check that the environment is correct
which python
python -c "import smp_bindings; print(smp_bindings.__file__)"
```

If this works but Pylance still fails, ensure the Python interpreter selected in VSCode matches the output of `which python`.

### Pylance shows "py.typed is missing"

This usually doesn't break imports but indicates incomplete type metadata. The file `smp_bindings/py.typed` (created during setup) resolves this. If you see the warning, ensure:

```bash
ls smp_bindings/py.typed
```

If it doesn't exist, create it:

```bash
touch smp_bindings/py.typed
```

## Development Workflow

Once setup is complete:

1. **Edit files** in `smp_bindings/` — Pylance will provide real-time feedback
2. **Import modules** from other projects — no need to modify `sys.path`; editable install handles it
3. **Run tests** — use your normal test runner (pytest, unittest, etc.)

```bash
pytest smp_bindings/  # example
```

## See Also

- [Pylance documentation](https://github.com/microsoft/pylance-release)
- [pyright configuration](https://github.com/microsoft/pyright/blob/main/docs/configuration.md)
