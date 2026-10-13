# Kubernetes manifests

```
app/                 colourless Deployment + Service template
colors/{blue,green}  stamp the template: -blue/-green suffix, `color` label on selectors
base/                both colours + the live Service `pulse` (selector color=blue|green)
overlays/{staging,production}  namespace, ECR image name, replica count
```

Rendered per namespace:

| Object | Purpose |
|---|---|
| `Deployment/pulse-blue`, `Deployment/pulse-green` | both colours, always running (instant rollback) |
| `Service/pulse-blue`, `Service/pulse-green` | reach one colour directly, for the pre-switch smoke test |
| `Service/pulse` (`role=live`) | live traffic; its `color` selector is the switch |

## Ownership contract with the deploy script

Kustomize owns the *shape*. The deploy script owns two fields:

- the **image tag** of the colour being deployed (the overlay's `set-by-deploy-script` tag is a deliberate placeholder)
- the **`color` selector** on `Service/pulse`

Never `kubectl apply -k overlays/<env>` wholesale after bootstrap: it would reset the live colour's image to the placeholder and the selector to `blue`. Apply one colour at a time with `-l color=<idle>`; the live Service carries no `color` label so it is never selected.

Pods pull from ECR via the `ecr-pull` Secret, which the deploy job recreates each run from a short-lived token (ECR tokens last 12 hours).
