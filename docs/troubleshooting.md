# Troubleshooting

| Symptom | Check/action |
|---|---|
| Deployment profile is missing | Run install.sh for Docker. In a source checkout, run `./scripts/start.sh --init-env` and select the intended profile |
| ECI startup lists missing credentials or cache ID | Check the selected ECI profile. An explicitly empty process environment value overrides the saved value. Use `buildeci.sh` to create a cache or supply an existing Ready cache ID |
| Host must be started by Controller | Open the account through Controller to start its Host |
| Host configuration changed | Use explicit host delete, then start; use external storage if data must survive |
| Account setting was saved but the running Host did not apply it | Retry the settings request or reopen the account to apply the Redis settings |
| ECI Running but controller never ready | Check private-IP reachability, TCP 8080, Host logs, the filesystem and the internal Host token |
| Web search is unconfigured | Save a DeepSeek search API key in **Settings → Web search** |
| Browser frontend CORS blocked | Open the UI from the Controller URL. For Vite, set the Controller’s exact loopback `--web-dev-origin` |
| POST failed after a disconnect | Read history/events before deciding to submit again; controller intentionally does not replay uncertain sends |
| Stop Host could not confirm ECI release | Check the reported cloud error and retry Stop Host. ECI instance storage lasts until release |
| Docker Host or Controller image missing | Rerun install.sh or explicitly `docker pull` the configured image. Neither Compose nor start.sh builds or pulls at startup |
| `/runtime.yaml` mount fails | Ensure `configs/default.yaml` exists beside the deployment's Compose file; install.sh downloads it from the repository |
| GHCR pull is denied | Maintainers must publish both packages and set each to Public. A public source repository does not make its images public |
| HTTPS build fails behind private CA | Use Docker BuildKit secret build_ca with your trusted CA; certificate verification remains enabled |

Use `controller check` for offline configuration validation, `controller doctor`
for read-only platform checks, `controller host status` for the saved resource
reference and `docker logs <exact-container-id>` for local Host startup failures.
Keep API keys and private Session contents out of shared diagnostic logs.
