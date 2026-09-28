package tracker

// TorrentCategory classifies torrents for per-category middleware dispatch.
type TorrentCategory string

const (
	CategoryDefault     TorrentCategory = ""
	CategoryPatch       TorrentCategory = "patch"
	CategoryML          TorrentCategory = "ml"
	CategoryBackup      TorrentCategory = "backup"
	CategoryScience     TorrentCategory = "science"
	CategoryOTA         TorrentCategory = "ota"
	CategoryLive        TorrentCategory = "live"
	CategoryPackage     TorrentCategory = "package"
	CategoryCompute     TorrentCategory = "compute"
	CategoryContainer   TorrentCategory = "container"
	CategoryBlockchain  TorrentCategory = "blockchain"
	CategoryAVMap       TorrentCategory = "avmap"
	CategoryDICOM       TorrentCategory = "dicom"
	CategoryThreatIntel TorrentCategory = "threatintel"
	CategoryTickData    TorrentCategory = "tickdata"
	CategoryArchive     TorrentCategory = "archive"
	CategoryCI          TorrentCategory = "ci"
	CategoryImagery     TorrentCategory = "imagery"
	CategoryGame        TorrentCategory = "game"
	CategoryEdgeAI      TorrentCategory = "edgeai"
)
