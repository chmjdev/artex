# Binteca Security workspace

This independent fork lives at `Binteca/Security/artex` for internal estate
penetration testing and further customization. Its source remote is
`https://github.com/chmjdev/artex`; its private build project is
`https://git.binteca.io/binteca/security/artex`.

## Build

`.gitlab-ci.yml` builds the static web interface and embeds it in the
Linux amd64 executable using the upstream `build.sh`. Jobs select the native
lab01 runner, ID 3 and tag `kvm`, with Go 1.26.11. The build job produces a
tar archive, binary/archive SHA-256 checksums and a source/job receipt under
`dist/`. No GitHub Actions or GitHub release is used.

## Zeus model smoke run

`ci/zeus-smoke.py` starts the actual compiled application on loopback with a
disposable PostgreSQL cluster on its own Unix socket. Authentication and
runtime keys belong only to this fixture and are removed when it ends.
The test verifies startup, anonymous protected-API rejection, and real
`POST /api/llm/test` responses through the Zeus super gateway on lab01
(`http://127.0.0.1:3188/v1`, canonical name `zeus.int`).

The selected models are:

- `experientiallabs/deepseek-v4.1-flash-uncensored`
- `experientiallabs/deepseek-v4.1-flash`
- `orcarouter/orca/orcacyber-zero-1.5`

Each model is tested in streaming and non-streaming mode. Artex's `ollama`
adapter provides its keyless OpenAI-compatible transport; Zeus keeps its own
provider credentials. The test copies no provider keys and refuses missing
model IDs. Results are published as `dist/model-smoke.json`; private server
logs, fixture credentials and the database are excluded from artifacts.

This smoke run starts no assessment tasks and contacts no scan targets. A
passing connection test establishes API compatibility, not assessment quality.
Persistent operation and estate scanning require actual JCDS runtime onboarding
and a defined assessment scope. Daybreak is not configured.
