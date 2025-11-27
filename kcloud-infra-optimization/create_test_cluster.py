#!/usr/bin/env python3
"""
"""
import os
import time
import json
from datetime import datetime
os.environ['OS_CLIENT_CONFIG_FILE'] = '/root/kcloud_opt/clouds.yaml'
from openstack_cluster_crud import OpenStackClusterCRUD, ClusterConfig
def create_test_cluster():
"""
    """
    print("Creating test cluster...")
    
    crud = OpenStackClusterCRUD()
    

    print("\nAvailable templates:")
    templates = crud.get_cluster_templates()
    for i, tmpl in enumerate(templates):
        print(f"  {i+1}. {tmpl['name']} (ID: {tmpl['id']})")
    

    template_id = None
    for tmpl in templates:
        if 'dev' in tmpl['name'].lower():
            template_id = tmpl['id']
            break
    
    if not template_id:
        template_id = templates[0]['id']
    
    print(f"Selected template: {template_id}")
    

    cluster_name = f"test-demo-{datetime.now().strftime('%m%d-%H%M')}"
    
    config = ClusterConfig(
        name=cluster_name,
        cluster_template_id=template_id,
        master_count=1,
        node_count=1,
        fixed_network="cloud-platform-selfservice",
        fixed_subnet="cloud-platform-selfservice-subnet",
        labels={
            "kube_dashboard_enabled": "false",
            "prometheus_monitoring": "false",
            "auto_scaling_enabled": "false"
        }
    )
    
    print(f"\nCluster configuration:")
    print(f"  Name: {cluster_name}")
    print(f"  Template: {template_id}")
    print(f"  Masters: {config.master_count}")
    print(f"  Workers: {config.node_count}")
    print(f"  Network: {config.fixed_network}")
    

    try:
        print(f"\nStarting cluster creation...")
        start_time = time.time()
        
        cluster = crud.create_cluster(config)
        
        elapsed = time.time() - start_time
        print(f"\nCluster created successfully in {elapsed:.1f} seconds!")
        print(f"  ID: {cluster.id}")
        print(f"  Name: {cluster.name}")
        print(f"  Status: {cluster.status}")
        
        return cluster
        
    except Exception as e:
        print(f"\nFailed to create cluster: {e}")
        return None


def monitor_cluster_creation(cluster_id):
    """
    print(f"\nMonitoring cluster creation: {cluster_id}")
    
    crud = OpenStackClusterCRUD()
    
    start_time = time.time()
    check_count = 0
    
    while True:
        try:
            cluster = crud.get_cluster(cluster_id)
            elapsed = time.time() - start_time
            check_count += 1
            
            print(f"  [{check_count:2d}] {elapsed/60:.1f}min - Status: {cluster.status}")
            

            if cluster.status in ["CREATE_COMPLETE"]:
                print(f"\nCluster creation completed!")
                print(f"  API Address: {cluster.api_address}")
                print(f"  Master Addresses: {cluster.master_addresses}")
                print(f"  Node Addresses: {cluster.node_addresses}")
                break
                
            elif "FAILED" in cluster.status or "ERROR" in cluster.status:
                print(f"\nCluster creation failed: {cluster.status}")
                break
                

            if elapsed > 3600:
                print(f"\nTimeout after 1 hour")
                break
                
            time.sleep(30)
            
        except Exception as e:
            print(f"  Error checking status: {e}")
            time.sleep(30)


def list_all_clusters():
    """
    print("\nCurrent clusters:")
    
    crud = OpenStackClusterCRUD()
    
    try:
        clusters = crud.list_clusters()
        
        if not clusters:
            print("  No clusters found")
            return
            
        for cluster in clusters:
            age_str = cluster.created_at
            print(f"  - {cluster.name}")
            print(f"    ID: {cluster.id}")
            print(f"    Status: {cluster.status}")
            print(f"    Nodes: {cluster.master_count}M + {cluster.node_count}W")
            print(f"    Created: {age_str}")
            print()
            
    except Exception as e:
        print(f"  Error: {e}")


if __name__ == "__main__":
    import sys
    
    print("="*60)
    print(" OpenStack Cluster Creation Test")
    print("="*60)
    

    list_all_clusters()
    

    if len(sys.argv) > 1 and sys.argv[1] == "--create":

        cluster = create_test_cluster()
        
        if cluster:
            print(f"\nTo monitor progress, run:")
            print(f"   python create_test_cluster.py --monitor {cluster.id}")
            
    elif len(sys.argv) > 2 and sys.argv[1] == "--monitor":

        cluster_id = sys.argv[2]
        monitor_cluster_creation(cluster_id)
        
    else:
        print("\nUsage:")
        print("  python create_test_cluster.py                    # List clusters")
        print("  python create_test_cluster.py --create          # Create new cluster")
        print("  python create_test_cluster.py --monitor <ID>    # Monitor cluster")
