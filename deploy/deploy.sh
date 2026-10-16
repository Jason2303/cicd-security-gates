#!/usr/bin/env bash
set -euo pipefail

# Namespace and Image passed from deploy.yml
NAMESPACE="$1"
IMAGE="$2"

# PID of the background tunnel
PF_PID=""

# Stop port forwarding 
stop_port_forward() {
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" 2>/dev/null || true
    PF_PID=""
  fi
}
# Run whenever the script ends and even on a crash
trap stop_port_forward EXIT

# Ask main Service which colour is live and which is Idle
LIVE=$(kubectl get svc pulse -n "$NAMESPACE" -o jsonpath='{.spec.selector.color}')
if [[ "$LIVE" == "blue" ]]; then
  IDLE="green"
else
  IDLE="blue"
fi

# Ask Kubernetes which Image is Live colour running based on the image of the first container in the pod template
LIVE_IMAGE=$(kubectl get deployment "pulse-$LIVE" -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].image}')
# Check within it, if LIVE_IMAGE ends with set-by-deploy-script, that means an image hasn't been placed yet
if [[ "$LIVE_IMAGE" == *set-by-deploy-script ]]; then
  BOOTSTRAP=true
else
  BOOTSTRAP=false
fi

# Print out the Live colour, Idle colour and Bootstrap value
echo "LIVE=$LIVE IDLE=$IDLE BOOTSTRAP=$BOOTSTRAP"

# Registry address
REGISTRY="${IMAGE%%/*}"

# Using the pull role credentials, ask AWS for a temporary ECR password 
ECR_PASSWORD=$(aws ecr get-login-password --region us-east-1)

# Create or update secret and save in cluster
kubectl create secret docker-registry ecr-pull \
  --docker-server="$REGISTRY" \
  --docker-username=AWS \
  --docker-password="$ECR_PASSWORD" \
  -n "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Set the image on the Idle Deployment
kubectl set image "deployment/pulse-$IDLE" pulse="$IMAGE" -n "$NAMESPACE"
# Wait up to 180s for all new Pods to be ready, else fail
kubectl rollout status "deployment/pulse-$IDLE" -n "$NAMESPACE" --timeout=180s

# Tunnel:- laptop:18080 TO idle colour's Service
kubectl port-forward "svc/pulse-$IDLE" 18080:80 -n "$NAMESPACE" >/dev/null 2>&1 &
# Process ID of Process just started
PF_PID=$!
# Give time for the connection to be created
sleep 3
# If condition incase the process died
if ! kill -0 "$PF_PID" 2>/dev/null; then
  echo "::error::port-forward on 18080 died (port already in use?)"
  exit 1
fi

# If the health check failed, it means the Idle pod has an issue and can't be used
if ! curl -fsS --retry 3 --retry-delay 2 --retry-all-errors http://127.0.0.1:18081/healthz >/dev/null; then
  echo "::error::Smoke test FAILED on $IDLE"
  exit 1
fi
echo
stop_port_forward
echo "Smoke test passed on $IDLE"

# Switch the service from the Live to the Idle using patch instead of apply
kubectl patch svc pulse -n "$NAMESPACE" -p "{\"spec\":{\"selector\":{\"color\":\"$IDLE\"}}}"
echo "Switched live traffic: $LIVE -> $IDLE"

# Tunnel:- laptop:18081 TO main pulse Service
kubectl port-forward svc/pulse 18081:80 -n "$NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3

# Assign true to Healthy
HEALTHY=true

# Tunnel died. Treats the deploy as failed and rolls back
if ! kill -0 "$PF_PID" 2>/dev/null; then
  echo "port-forward on 18081 died: cannot verify, treating as unhealthy"
  HEALTHY=false
fi

# Health check for the live deployment
for i in $(seq 1 30); do
  [[ "$HEALTHY" == "true" ]] || break
  if ! curl -fsS --retry 2 --retry-delay 2 --retry-connrefused http://127.0.0.1:18081/healthz >/dev/null; then
    echo "Health check $i/30 FAILED"
    HEALTHY=false
    break
  fi
  echo "Health check $i/30 OK"
  sleep 10
done
stop_port_forward

# If health check is Good print Idle is now Live
if [[ "$HEALTHY" == "true" ]]; then
  echo "Deploy OK: $IDLE is live"
  exit 0
fi

# The start. No good colour to roll back to so fail.
if [[ "$BOOTSTRAP" == "true" ]]; then
  echo "Health check failed on first deploy: no good colour to roll back to"
  exit 1
fi

# Roll back to the colour that was live at the start. Retry: a busy cluster can refuse one attempt
for attempt in 1 2 3 4 5 6; do
  if kubectl patch svc pulse -n "$NAMESPACE" -p "{\"spec\":{\"selector\":{\"color\":\"$LIVE\"}}}"; then
    echo "ROLLED BACK: live traffic returned to $LIVE"
    exit 1
  fi
  echo "Rollback attempt $attempt/6 failed, retrying in 10 s"
  sleep 10
done
echo "::error::ROLLBACK FAILED: live traffic is still on $IDLE. Patch the pulse Service back to $LIVE by hand"
exit 1

