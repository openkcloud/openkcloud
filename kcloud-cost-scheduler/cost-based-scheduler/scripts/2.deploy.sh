#!/usr/bin/env bash

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# $1 is create/a or delete/d

if [ "$1" == "delete" ] || [ "$1" == "d" ]; then
    echo "================================"
    echo "Deleting Cost Based Scheduler deployment..."
    echo "================================"
    kubectl delete -f "$dir/../deployments/cost-based-scheduler.yaml"

    if [ $? -eq 0 ]; then
        echo "Deployment deleted successfully"
    else
        echo "Error: Deployment deletion failed"
        exit 1
    fi

elif [ "$1" == "create" ] || [ "$1" == "a" ]; then
    echo "================================"
    echo "Createing Cost Based Scheduler deployment..."
    echo "================================"
    kubectl create -f "$dir/../deployments/cost-based-scheduler.yaml"

    if [ $? -eq 0 ]; then
        echo "Deployment created successfully"
        echo ""
        echo "Checking pod status..."
        sleep 3
        kubectl get pods -n kcp-control-plane -l app=cost-based-scheduler
    else
        echo "Error: Deployment create failed"
        exit 1
    fi

else
    echo "Usage: $0 [create|c|delete|d]"
    echo ""
    echo "Examples:"
    echo "  $0 create   # create deployment"
    echo "  $0 c        # create deployment (short)"
    echo "  $0 delete   # Delete deployment"
    echo "  $0 d        # Delete deployment (short)"
    exit 1
fi
