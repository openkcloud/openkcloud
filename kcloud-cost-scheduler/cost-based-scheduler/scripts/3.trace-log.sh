#!/usr/bin/env bash

NAMESPACE="kcp-control-plane"
LABEL="app=cost-based-scheduler"

echo "================================"
echo "Tracing Cost Based Scheduler logs"
echo "Namespace: $NAMESPACE"
echo "Label: $LABEL"
echo "================================"
echo ""

# Wait for pod to be running
echo "Waiting for pod to be ready..."
while true; do
    PODNAME=$(kubectl get pods -n ${NAMESPACE} -l ${LABEL} --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

    if [ -n "$PODNAME" ]; then
        echo "Found running pod: $PODNAME"
        echo ""
        break
    fi

    echo -n "."
    sleep 2
done

# Follow logs
echo "Following logs (Ctrl+C to exit)..."
echo "================================"
kubectl logs -n ${NAMESPACE} -l ${LABEL} -f --tail=50
