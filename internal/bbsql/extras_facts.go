// extras_facts.go: the slow-moving facts of an instance, read once a day.
//
//	bb:facts     version, edition kind, cores, memory, uptime (one row)
//	bb:factsdb   database options, one row per database (numeric id, no names)
//	bb:factscfg  sp_configure values, one row per setting (numeric id, no names)
//
// Sources, checked on Microsoft Learn:
//   - sys.dm_os_sys_info (cpu_count, hyperthread_ratio, scheduler_count,
//     max_workers_count, physical_memory_kb, committed_target_kb,
//     virtual_machine_type int, sqlserver_start_time datetime) with
//     SERVERPROPERTY for ProductVersion, EngineEdition, IsClustered and
//     IsHadrEnabled
//   - sys.databases (database_id, compatibility_level, recovery_model, state,
//     user_access, page_verify_option, snapshot_isolation_state and the bit
//     options; is_query_store_on needs SQL Server 2016 or later)
//   - sys.configurations (configuration_id int, value_in_use sql_variant that
//     always holds an integer)
//
// Permission: VIEW SERVER STATE for dm_os_sys_info (VIEW SERVER PERFORMANCE
// STATE on SQL Server 2022 and later), VIEW ANY DATABASE for the database list.
// Both are inside the C8 ceiling.
//
// No server name, machine name, instance name, database name, collation text or
// edition text is read. The edition is the numeric EngineEdition (2 Standard, 3
// Enterprise including Developer and Evaluation, 4 Express). Settings and
// database options carry their ids only: the reader maps an id to its name from
// the public configuration list, so no name travels in a sample.
package bbsql

import (
	"errors"
	"reflect"
	"regexp"
	"time"
)

const (
	CapFacts    = CapSingleRow
	CapFactsDB  = 512 // bb:factsdb fetches TOP 513, keeps 512
	CapFactsCfg = 256 // bb:factscfg fetches TOP 257, keeps 256
)

const stmtFacts = setPrefix + `SELECT CAST(SERVERPROPERTY(N'ProductVersion') AS nvarchar(32)) AS f_version,
  CAST(SERVERPROPERTY(N'EngineEdition') AS int) AS f_engine_edition,
  CAST(SERVERPROPERTY(N'IsClustered') AS int) AS f_clustered,
  CAST(SERVERPROPERTY(N'IsHadrEnabled') AS int) AS f_hadr,
  cpu_count AS f_cpus,
  hyperthread_ratio AS f_ht_ratio,
  scheduler_count AS f_schedulers,
  max_workers_count AS f_max_workers,
  physical_memory_kb AS f_phys_kb,
  committed_target_kb AS f_target_kb,
  virtual_machine_type AS f_vm_type,
  DATEDIFF(second, sqlserver_start_time, GETDATE()) AS f_uptime_sec
FROM sys.dm_os_sys_info /* unravis:bb:facts */`

const stmtFactsDB = setPrefix + `SELECT TOP 513 database_id, compatibility_level AS fdb_compat,
  recovery_model AS fdb_recovery, state AS fdb_state, user_access AS fdb_access,
  page_verify_option AS fdb_page_verify, snapshot_isolation_state AS fdb_snapshot,
  CASE WHEN is_read_only = 1 THEN 1 ELSE 0 END AS fdb_read_only,
  CASE WHEN is_auto_close_on = 1 THEN 1 ELSE 0 END AS fdb_auto_close,
  CASE WHEN is_auto_shrink_on = 1 THEN 1 ELSE 0 END AS fdb_auto_shrink,
  CASE WHEN is_auto_create_stats_on = 1 THEN 1 ELSE 0 END AS fdb_auto_create_stats,
  CASE WHEN is_auto_update_stats_on = 1 THEN 1 ELSE 0 END AS fdb_auto_update_stats,
  CASE WHEN is_read_committed_snapshot_on = 1 THEN 1 ELSE 0 END AS fdb_rcsi,
  CASE WHEN is_query_store_on = 1 THEN 1 ELSE 0 END AS fdb_query_store,
  CASE WHEN is_encrypted = 1 THEN 1 ELSE 0 END AS fdb_encrypted
FROM sys.databases
ORDER BY database_id /* unravis:bb:factsdb */`

const stmtFactsCfg = setPrefix + `SELECT TOP 257 configuration_id AS cfg_id, CAST(value_in_use AS bigint) AS cfg_value
FROM sys.configurations
ORDER BY configuration_id /* unravis:bb:factscfg */`

// versionRe is the shape of ProductVersion: 2 to 4 dot-separated numbers.
var (
	versionRe     = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,6}){1,3}$`)
	errFactsShape = errors.New("bbsql: invalid facts value")
)

// Facts is the bb:facts payload.
type Facts struct {
	Version       string `json:"fVersion"`
	EngineEdition uint8  `json:"fEngineEdition"`
	Clustered     uint8  `json:"fClustered"`
	Hadr          uint8  `json:"fHadr"`
	CPUs          uint32 `json:"fCpus"`
	HTRatio       uint32 `json:"fHtRatio"`
	Schedulers    uint32 `json:"fSchedulers"`
	MaxWorkers    uint32 `json:"fMaxWorkers"`
	PhysKb        uint64 `json:"fPhysKb"`
	TargetKb      uint64 `json:"fTargetKb"`
	VMType        uint8  `json:"fVmType"`
	UptimeSec     uint64 `json:"fUptimeSec"`
}

type factsSampler struct{}

func (factsSampler) ID() string              { return "bb:facts" }
func (factsSampler) Kind() string            { return "facts" }
func (factsSampler) Interval() time.Duration { return ExtraEveryDay }

func (factsSampler) Decode(rows []Row) (any, bool, error) {
	var out []Facts
	truncated, err := decodeRows(rows, &out, CapFacts)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	f := out[0]
	if !versionRe.MatchString(f.Version) || f.Clustered > 1 || f.Hadr > 1 {
		return nil, false, errFactsShape
	}
	return f, truncated, nil
}

// FactsDB is one database's options, by numeric id.
type FactsDB struct {
	DatabaseID uint32 `json:"databaseId"`
	Compat     uint16 `json:"fdbCompat"`
	Recovery   uint8  `json:"fdbRecovery"`
	State      uint8  `json:"fdbState"`
	Access     uint8  `json:"fdbAccess"`
	PageVerify uint8  `json:"fdbPageVerify"`
	Snapshot   uint8  `json:"fdbSnapshot"`
	ReadOnly   uint8  `json:"fdbReadOnly"`
	AutoClose  uint8  `json:"fdbAutoClose"`
	AutoShrink uint8  `json:"fdbAutoShrink"`
	AutoCreate uint8  `json:"fdbAutoCreateStats"`
	AutoUpdate uint8  `json:"fdbAutoUpdateStats"`
	RCSI       uint8  `json:"fdbRcsi"`
	QueryStore uint8  `json:"fdbQueryStore"`
	Encrypted  uint8  `json:"fdbEncrypted"`
}

type factsDBSampler struct{}

func (factsDBSampler) ID() string              { return "bb:factsdb" }
func (factsDBSampler) Kind() string            { return "factsdb" }
func (factsDBSampler) Interval() time.Duration { return ExtraEveryDay }

func (factsDBSampler) Decode(rows []Row) (any, bool, error) {
	var out []FactsDB
	truncated, err := decodeRows(rows, &out, CapFactsDB)
	if err != nil {
		return nil, false, err
	}
	for _, d := range out {
		if d.ReadOnly > 1 || d.AutoClose > 1 || d.AutoShrink > 1 || d.AutoCreate > 1 || d.AutoUpdate > 1 ||
			d.RCSI > 1 || d.QueryStore > 1 || d.Encrypted > 1 {
			return nil, false, errFactsShape
		}
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

// FactsCfg is one sp_configure value, by numeric configuration id.
type FactsCfg struct {
	ID    uint32 `json:"cfgId"`
	Value int64  `json:"cfgValue"`
}

type factsCfgSampler struct{}

func (factsCfgSampler) ID() string              { return "bb:factscfg" }
func (factsCfgSampler) Kind() string            { return "factscfg" }
func (factsCfgSampler) Interval() time.Duration { return ExtraEveryDay }

func (factsCfgSampler) Decode(rows []Row) (any, bool, error) {
	var out []FactsCfg
	truncated, err := decodeRows(rows, &out, CapFactsCfg)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:facts", stmtFacts, CapFacts, "sys.dm_os_sys_info")
	registerExtraColumns(map[string]string{
		"f_version": "fVersion", "f_engine_edition": "fEngineEdition", "f_clustered": "fClustered", "f_hadr": "fHadr",
		"f_cpus": "fCpus", "f_ht_ratio": "fHtRatio", "f_schedulers": "fSchedulers", "f_max_workers": "fMaxWorkers",
		"f_phys_kb": "fPhysKb", "f_target_kb": "fTargetKb", "f_vm_type": "fVmType", "f_uptime_sec": "fUptimeSec",
	})
	registerExtraRowSpec(reflect.TypeOf(Facts{}), specAll(
		"fVersion", "fEngineEdition", "fClustered", "fHadr", "fCpus", "fHtRatio", "fSchedulers",
		"fMaxWorkers", "fPhysKb", "fTargetKb", "fVmType", "fUptimeSec"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &factsSampler{} })

	registerExtraStatement("bb:factsdb", stmtFactsDB, CapFactsDB, "sys.databases")
	registerExtraColumns(map[string]string{
		"fdb_compat": "fdbCompat", "fdb_recovery": "fdbRecovery", "fdb_state": "fdbState", "fdb_access": "fdbAccess",
		"fdb_page_verify": "fdbPageVerify", "fdb_snapshot": "fdbSnapshot", "fdb_read_only": "fdbReadOnly",
		"fdb_auto_close": "fdbAutoClose", "fdb_auto_shrink": "fdbAutoShrink", "fdb_auto_create_stats": "fdbAutoCreateStats",
		"fdb_auto_update_stats": "fdbAutoUpdateStats", "fdb_rcsi": "fdbRcsi", "fdb_query_store": "fdbQueryStore",
		"fdb_encrypted": "fdbEncrypted",
	})
	registerExtraRowSpec(reflect.TypeOf(FactsDB{}), specAll(
		"databaseId", "fdbCompat", "fdbRecovery", "fdbState", "fdbAccess", "fdbPageVerify", "fdbSnapshot",
		"fdbReadOnly", "fdbAutoClose", "fdbAutoShrink", "fdbAutoCreateStats", "fdbAutoUpdateStats", "fdbRcsi",
		"fdbQueryStore", "fdbEncrypted"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &factsDBSampler{} })

	registerExtraStatement("bb:factscfg", stmtFactsCfg, CapFactsCfg, "sys.configurations")
	registerExtraColumns(map[string]string{"cfg_id": "cfgId", "cfg_value": "cfgValue"})
	registerExtraRowSpec(reflect.TypeOf(FactsCfg{}), specAll("cfgId", "cfgValue"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &factsCfgSampler{} })
}
