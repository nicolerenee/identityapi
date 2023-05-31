// Copyright Infratographer, Inc. and/or licensed to Infratographer, Inc. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.
//
// Code generated by entc, DO NOT EDIT.

package migrate

import (
	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
)

var (
	// TenantsColumns holds the columns for the "tenants" table.
	TenantsColumns = []*schema.Column{
		{Name: "id", Type: field.TypeString, Unique: true},
		{Name: "created_at", Type: field.TypeTime},
		{Name: "updated_at", Type: field.TypeTime},
		{Name: "name", Type: field.TypeString},
		{Name: "description", Type: field.TypeString, Nullable: true},
		{Name: "parent_tenant_id", Type: field.TypeString, Nullable: true},
	}
	// TenantsTable holds the schema information for the "tenants" table.
	TenantsTable = &schema.Table{
		Name:       "tenants",
		Columns:    TenantsColumns,
		PrimaryKey: []*schema.Column{TenantsColumns[0]},
		ForeignKeys: []*schema.ForeignKey{
			{
				Symbol:     "tenants_tenants_children",
				Columns:    []*schema.Column{TenantsColumns[5]},
				RefColumns: []*schema.Column{TenantsColumns[0]},
				OnDelete:   schema.SetNull,
			},
		},
		Indexes: []*schema.Index{
			{
				Name:    "tenant_created_at",
				Unique:  false,
				Columns: []*schema.Column{TenantsColumns[1]},
			},
			{
				Name:    "tenant_updated_at",
				Unique:  false,
				Columns: []*schema.Column{TenantsColumns[2]},
			},
		},
	}
	// Tables holds all the tables in the schema.
	Tables = []*schema.Table{
		TenantsTable,
	}
)

func init() {
	TenantsTable.ForeignKeys[0].RefTable = TenantsTable
}
