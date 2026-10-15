#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="$1"
IMAGE="$2"

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


kubectl set image "deployment/pulse-$IDLE" pulse="$IMAGE" -n "$NAMESPACE"
kubectl rollout status "deployment/pulse-$IDLE" -n "$NAMESPACE" --timeout=180s
kubectl port-forward "svc/pulse-$IDLE" 18080:80 -n "$NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3

curl -fsS --retry 5 --retry-delay 2 --retry-connrefused http://127.0.0.1:18080/healthz \
  || { kill "$PF_PID"; echo "Smoke test FAILED on	 $IDLE"; exit 1; }
echo
kill "$PF_PID" 2>/dev/null || true
echo "Smoke test passed on $IDLE"

kubectl patch svc pulse -n "$NAMESPACE" -p "{\"spec\":{\"selector\":{\"color\":\"$IDLE\"}}}"
echo "Switched live traffic: $LIVE -> $IDLE"

kubectl port-forward svc/pulse 18081:80 -n "$NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3

HEALTHY=true
for i in $(seq 1 30); do
  if ! curl -fsS --retry 2 --retry-delay 2 --retry-connrefused http://127.0.0.1:18081/healthz >/dev/null; then
    echo "Health check $i/30 FAILED"
    HEALTHY=false
    break
  fi
  echo "Health check $i/30 OK"
  sleep 10
done
kill "$PF_PID" 2>/dev/null || true

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

