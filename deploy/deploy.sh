#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="$1"
IMAGE="$2"

PF_PID=""
stop_port_forward() {
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" 2>/dev/null || true
    PF_PID=""
  fi
}
trap stop_port_forward EXIT

LIVE=$(kubectl get svc pulse -n "$NAMESPACE" -o jsonpath='{.spec.selector.color}')
if [[ "$LIVE" == "blue" ]]; then
  IDLE="green"
else
  IDLE="blue"
fi
LIVE_IMAGE=$(kubectl get deployment "pulse-$LIVE" -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].image}')
if [[ "$LIVE_IMAGE" == *set-by-deploy-script ]]; then
  BOOTSTRAP=true
else
  BOOTSTRAP=false
fi
echo "LIVE=$LIVE IDLE=$IDLE BOOTSTRAP=$BOOTSTRAP"
REGISTRY="${IMAGE%%/*}"
ECR_PASSWORD=$(aws ecr get-login-password --region us-east-1)

kubectl create secret docker-registry ecr-pull \
  --docker-server="$REGISTRY" \
  --docker-username=AWS \
  --docker-password="$ECR_PASSWORD" \
  -n "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -


if [[ "${INJECT_FAILURE:-false}" == "true" ]]; then
  echo "ROLLBACK TEST: $IDLE will fail /healthz 90 s after its pods start"
  kubectl set env "deployment/pulse-$IDLE" -n "$NAMESPACE" FAIL_HEALTH=true FAIL_HEALTH_AFTER=90s
else
  kubectl set env "deployment/pulse-$IDLE" -n "$NAMESPACE" FAIL_HEALTH- FAIL_HEALTH_AFTER-
fi

kubectl set image "deployment/pulse-$IDLE" pulse="$IMAGE" -n "$NAMESPACE"
kubectl rollout status "deployment/pulse-$IDLE" -n "$NAMESPACE" --timeout=180s
kubectl port-forward "svc/pulse-$IDLE" 18080:80 -n "$NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3
if ! kill -0 "$PF_PID" 2>/dev/null; then
  echo "::error::port-forward on 18080 died (port already in use?)"
  exit 1
fi

if ! curl -fsS --retry 5 --retry-delay 2 --retry-connrefused http://127.0.0.1:18080/healthz; then
  echo "::error::Smoke test FAILED on $IDLE"
  exit 1
fi
echo
stop_port_forward
echo "Smoke test passed on $IDLE"

kubectl patch svc pulse -n "$NAMESPACE" -p "{\"spec\":{\"selector\":{\"color\":\"$IDLE\"}}}"
echo "Switched live traffic: $LIVE -> $IDLE"

kubectl port-forward svc/pulse 18081:80 -n "$NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3

HEALTHY=true
if ! kill -0 "$PF_PID" 2>/dev/null; then
  echo "port-forward on 18081 died: cannot verify, treating as unhealthy"
  HEALTHY=false
fi
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

if [[ "$HEALTHY" == "true" ]]; then
  echo "Deploy OK: $IDLE is live"
  exit 0
fi

if [[ "$BOOTSTRAP" == "true" ]]; then
  echo "Health check failed on first deploy: no good colour to roll back to"
  exit 1
fi

kubectl patch svc pulse -n "$NAMESPACE" -p "{\"spec\":{\"selector\":{\"color\":\"$LIVE\"}}}"
echo "ROLLED BACK: live traffic returned to $LIVE"
exit 1

