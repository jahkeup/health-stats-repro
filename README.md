# Repro Runner

This is a repro case for a [Docker/Moby issue](https://github.com/moby/moby/issues/36661) where a Container with HEALTHCHECKs may fall into a state where it cannot be interacted with with any API calls.

## Running

Requirements:

- Docker: v17.12.0 - v18.03.0-rc4
- Golang: >1.8
- make

```bash
# Run the runner with 20 concurrent processes to increase odds of
# bumping into the bug.
make run N=20
```

## Tested against

### Ubuntu

| Package                   | Result |
|---------------------------|--------|
| `17.09.1~ce-0~ubuntu`     | pass   |
| `17.10.0~ce-0~ubuntu`     | pass   |
| `17.11.0~ce-0~ubuntu`     | pass   |
| `17.12.0~ce~rc1-0~ubuntu` | fail   |
| `17.12.0~ce-0~ubuntu`     | fail   |
| `17.12.1~ce-0~ubuntu`     | fail   |
| `18.01.0~ce-0~ubuntu`     | fail   |
| `18.02.0~ce-0~ubuntu`     | fail   |
| `18.03.0~ce~rc4-0~ubuntu` | fail   |

## Amazon Linux

| Package      | Result |
|--------------|--------|
| `17.12.0-ce` | fail   |
| `17.09.1-ce` | pass   |
