kubectl create configmap dockerfile-config --from-file=Dockerfile=$(pwd)/Dockerfile

# Apply the Kaniko pod configuration
kubectl apply -f kaniko.yaml

kubectl logs kaniko --follow

# Wait for the pod to complete (optional: you can use a loop to check the status)
while [ "$(kubectl get pod kaniko -o jsonpath='{.status.phase}')" != "Succeeded" ]; do
  kubectl logs -f kaniko --follow
  sleep 1
  if [ "$(kubectl get pod kaniko -o jsonpath='{.status.phase}')" == "Failed" ]; then
    echo "Kaniko pod failed"
    kubectl delete pod kaniko
    kubectl delete configmap dockerfile-config
    exit 1
  fi
done

echo "Kaniko pod completed successfully"

# Delete the Kaniko pod
kubectl delete pod kaniko
kubectl delete configmap dockerfile-config
