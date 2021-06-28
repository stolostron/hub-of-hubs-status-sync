// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package dbsyncers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v4/pgxpool"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	dbEnumCompliant    = "compliant"
	dbEnumNonCompliant = "non_compliant"
)

type policyDBSyncer struct {
	client                 client.Client
	log                    logr.Logger
	databaseConnectionPool *pgxpool.Pool
	syncInterval           time.Duration
	tableName              string
	specTableName          string
}

func (syncer *policyDBSyncer) Start(stopChannel <-chan struct{}) error {
	ticker := time.NewTicker(syncer.syncInterval)

	for {
		select {
		case <-stopChannel:
			ticker.Stop()

			syncer.log.Info("stop performing sync", "table", syncer.tableName)

			return nil
		case <-ticker.C:
			syncer.sync()
		}
	}
}

func (syncer *policyDBSyncer) sync() {
	syncer.log.Info("performing sync", "table", syncer.tableName)

	rows, _ := syncer.databaseConnectionPool.Query(context.Background(),
		fmt.Sprintf(`SELECT id, payload -> 'metadata' ->> 'name' as name, payload -> 'metadata' ->> 'namespace'
			    as namespace FROM spec.%s WHERE deleted = FALSE`, syncer.specTableName))

	for rows.Next() {
		var id, name, namespace string

		err := rows.Scan(&id, &name, &namespace)
		if err != nil {
			syncer.log.Error(err, "error in select", "table", syncer.specTableName)
			continue
		}

		instance := &policiesv1.Policy{}
		err = syncer.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, instance)

		if err != nil {
			syncer.log.Error(err, "error in getting CR", "name", name, "namespace", namespace)
			continue
		}

		syncer.handlePolicy(instance)
	}
}

func (syncer *policyDBSyncer) handlePolicy(instance *policiesv1.Policy) {
	syncer.log.Info("handling a policy", "policy", instance, "uid", string(instance.GetUID()))

	rows, _ := syncer.databaseConnectionPool.Query(context.Background(),
		fmt.Sprintf(`SELECT cluster_name, leaf_hub_name, compliance FROM status.%s
			     WHERE policy_id = '%s' ORDER BY leaf_hub_name, cluster_name`,
			syncer.tableName, string(instance.GetUID())))

	compliancePerClusterStatuses := []*policiesv1.CompliancePerClusterStatus{}
	hasNonCompliantClusters := false
	dbEnumToPolicyComplianceStateMap := map[string]policiesv1.ComplianceState{
		dbEnumCompliant:    policiesv1.Compliant,
		dbEnumNonCompliant: policiesv1.NonCompliant,
	}

	for rows.Next() {
		var clusterName, leafHubName, complianceInDB string

		err := rows.Scan(&clusterName, &leafHubName, &complianceInDB)
		if err != nil {
			syncer.log.Error(err, "error in select", "table", syncer.tableName)
			continue
		}

		syncer.log.Info("handling a line in compliance table", "clusterName", clusterName,
			"leafHubName", leafHubName, "compliance", complianceInDB)

		var compliance policiesv1.ComplianceState = ""
		if mappedCompliance, ok := dbEnumToPolicyComplianceStateMap[complianceInDB]; ok {
			compliance = mappedCompliance
		}

		if compliance == policiesv1.NonCompliant {
			hasNonCompliantClusters = true
		}

		compliancePerClusterStatuses = append(compliancePerClusterStatuses, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  compliance,
			ClusterName:      clusterName,
			ClusterNamespace: leafHubName,
		})
	}

	syncer.log.Info("calculated compliance", "compliancePerClusterStatuses", compliancePerClusterStatuses)

	originalInstance := instance.DeepCopy()
	instance.Status.Status = compliancePerClusterStatuses
	instance.Status.ComplianceState = ""

	if hasNonCompliantClusters {
		instance.Status.ComplianceState = policiesv1.NonCompliant
	} else if len(compliancePerClusterStatuses) > 0 {
		instance.Status.ComplianceState = policiesv1.Compliant
	}

	err := syncer.client.Status().Patch(context.Background(), instance, client.MergeFrom(originalInstance))
	if err != nil && !errors.IsNotFound(err) {
		syncer.log.Error(err, "Failed to update policy status...")
	}
}

func addPolicyDBSyncer(mgr ctrl.Manager, databaseConnectionPool *pgxpool.Pool, syncInterval time.Duration) error {
	return mgr.Add(&policyDBSyncer{
		client:                 mgr.GetClient(),
		log:                    ctrl.Log.WithName("policy-db-syncer"),
		databaseConnectionPool: databaseConnectionPool,
		syncInterval:           syncInterval,
		tableName:              "compliance",
		specTableName:          "policies",
	})
}