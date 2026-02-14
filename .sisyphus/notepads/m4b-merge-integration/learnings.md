# m4b-merge Integration Learnings

## Session Started
- **Date**: 2026-02-14
- **Branch**: feature/rust-m4b-merge
- **Session**: ses_3a26f792dffeC4lTRGbr7Jv24R

## Conventions

### Git Workflow
- All work on feature/rust-m4b-merge branch
- Atomic commits per task
- Conventional commit format: `type(scope): description`

### Code Patterns
- subprocess.run() with list arguments (NOT shell=True)
- Timeout default: 14400 seconds (4 hours)
- Path resolution: relative → absolute before subprocess
- Error handling: capture stdout/stderr, check exit codes

## Configuration Mapping
| Django Setting | Rust CLI Arg |
|----------------|--------------|
| Setting.api_url | --api-url |
| Setting.output_directory | --output / -o |
| Setting.completed_directory | --completed-directory |
| Setting.num_cpus | --num-cpus |
| Setting.output_scheme | --path-format / -p |
| Book.asin | --asin / -a |
| Book.src_path | --inputs / -i |

## Rust CLI Arguments (from main.rs)
```
-i, --inputs PATH           Input files or directories (required)
-o, --output PATH           Output directory for merged files
--api-url URL               Audnexus API URL (default: https://api.audnex.us)
--completed-directory PATH  Directory to move original files after processing
--num-cpus N                Number of CPUs (default: 1)
--log-level LEVEL           Logging level (default: info)
-p, --path-format TEMPLATE  Output path template (default: {author}/{title})
-a, --asin ASIN             ASIN for metadata lookup (optional)
--dry-run                   Show what would be done
--check-ffmpeg              Check FFmpeg installation
```

## Rust Output Format (lines 145-161 of main.rs)
On success:
```
=== Processing Complete ===
Successfully processed N audiobook(s)

1. /path/to/output/file.m4b
   Input files: N
   Metadata applied: Yes/No
   Source files moved: Yes/No
```

Output path extraction pattern: `r"^\d+\.\s+(.+)$"`

## Implementation Details

### Changes Made to utils/merge.py
1. **Removed imports**: `from m4b_merge import audible_helper, config, helpers, m4b_helper`
2. **Added imports**: `import subprocess`, `import requests`
3. **Modified `set_configs()`**: Returns CLI args dict instead of setting Python config attributes
4. **Modified `run_m4b_merge(asin)`**:
   - Builds CLI arguments list for subprocess
   - Calls `subprocess.run()` with list argument (NOT shell=True)
   - Timeout: 14400 seconds (4 hours)
   - Captures stdout/stderr via `capture_output=True`
   - Parses Rust output to extract output path
   - Updates book.status based on subprocess exit code
5. **Added `fetch_audible_metadata()`**: Direct HTTP calls to Audible API using requests

### Key Implementation Decisions
- Used `requests` library (already in requirements.txt) for Audible API calls instead of audible_helper
- Absolute path resolution via `Path(book.src_path).resolve()` before subprocess
- Regex pattern matching for output path extraction from Rust stdout
- Fallback to src_path if output parsing fails

## Task Status
- [x] Task 1: Create subprocess wrapper in utils/merge.py
- [x] Task 3: Update Dockerfile with Rust binary (COMPLETED 2026-02-14)

## Dockerfile Changes

### Issue Identified
The base image `ghcr.io/djdembeck/m4b-merge:develop` is based on Alpine Linux (musl libc), but the Rust binary built on Debian (glibc) was incompatible. This caused library linking errors:
- `Error loading shared library ld-linux-x86-64.so.2`
- `Error relocating: __isoc23_sscanf: symbol not found`

### Solution Implemented
Added multi-stage build with Alpine-based Rust builder to create a statically linked musl binary.

**Key Changes to docker/Dockerfile:**
1. Added build stage using `rust:1.93-alpine` for musl compatibility
2. Install static OpenSSL libs: `openssl-dev`, `openssl-libs-static`
3. Set `RUSTFLAGS="-C target-feature=+crt-static"` for fully static binary
4. Build with `--target x86_64-unknown-linux-musl`
5. Copy from musl target path: `target/x86_64-unknown-linux-musl/release/m4b-merge`

### Verification Results
- ✅ Binary location: `/usr/local/bin/m4b-merge`
- ✅ Binary type: Statically linked Rust executable (13MB)
- ✅ Binary version: `m4b-merge 0.1.0`
- ✅ FFmpeg available: version 5.0.1
- ✅ `m4b-merge --help` works correctly
- ✅ Docker build completes successfully (~3 minutes)

### Key Learnings
1. Alpine Linux uses musl libc, not glibc - binaries must be statically linked or built with musl
2. Using `rust:1.93-alpine` builder with `x86_64-unknown-linux-musl` target ensures compatibility
3. `RUSTFLAGS="-C target-feature=+crt-static"` creates fully static binary
4. The binary replaces the legacy Python wrapper at `/usr/local/bin/m4b-merge`
