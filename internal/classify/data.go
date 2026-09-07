package classify

import "strings"

// Dữ liệu tham chiếu cho bộ phân loại. Tất cả nằm trong code chứ không trong CSDL:
// đây là kiến thức về hạ tầng adtech, thay đổi theo nhịp phát hành chứ không theo
// nhịp vận hành, và sửa nó phải qua review giống như sửa một quy tắc.

// adtechDomains ánh xạ eTLD+1 của hạ tầng adtech đã biết sang phân loại của nó.
// Phân loại ở đây quan trọng: một domain trỏ CNAME vào eulerian.net là tracking,
// trỏ vào doubleclick.net là ads. Nhãn đi theo đích thật, không theo tên gọi.
var adtechDomains = map[string]Category{
	// Ad exchange, RTB, ad serving
	"doubleclick.net":       CategoryAds,
	"googlesyndication.com": CategoryAds,
	"googleadservices.com":  CategoryAds,
	"adnxs.com":             CategoryAds,
	"adsrvr.org":            CategoryAds,
	"rubiconproject.com":    CategoryAds,
	"pubmatic.com":          CategoryAds,
	"openx.net":             CategoryAds,
	"criteo.com":            CategoryAds,
	"criteo.net":            CategoryAds,
	"adform.net":            CategoryAds,
	"taboola.com":           CategoryAds,
	"outbrain.com":          CategoryAds,
	"smartadserver.com":     CategoryAds,
	"casalemedia.com":       CategoryAds,
	"33across.com":          CategoryAds,
	"sharethrough.com":      CategoryAds,
	"teads.tv":              CategoryAds,
	"admob.com":             CategoryAds,
	"applovin.com":          CategoryAds,
	"unityads.unity3d.com":  CategoryAds,
	"adcolony.com":          CategoryAds,
	"vungle.com":            CategoryAds,
	"inmobi.com":            CategoryAds,
	"mgid.com":              CategoryAds,
	"revcontent.com":        CategoryAds,
	"zedo.com":              CategoryAds,
	"admicro.vn":            CategoryAds,
	"adtima.vn":             CategoryAds,
	"eclick.vn":             CategoryAds,

	// Analytics, đo lường, CNAME cloaking
	"eulerian.net":          CategoryTracking,
	"scorecardresearch.com": CategoryTracking,
	"quantserve.com":        CategoryTracking,
	"chartbeat.com":         CategoryTracking,
	"segment.io":            CategoryTracking,
	"segment.com":           CategoryTracking,
	"mixpanel.com":          CategoryTracking,
	"amplitude.com":         CategoryTracking,
	"hotjar.com":            CategoryTracking,
	"fullstory.com":         CategoryTracking,
	"mouseflow.com":         CategoryTracking,
	"crazyegg.com":          CategoryTracking,
	"branch.io":             CategoryTracking,
	"adjust.com":            CategoryTracking,
	"appsflyer.com":         CategoryTracking,
	"kochava.com":           CategoryTracking,
	"tealium.com":           CategoryTracking,
	"tealiumiq.com":         CategoryTracking,
	"krxd.net":              CategoryTracking,
	"demdex.net":            CategoryTracking,
	"omtrdc.net":            CategoryTracking,
	"everesttech.net":       CategoryTracking,
	"2o7.net":               CategoryTracking,
	"bluekai.com":           CategoryTracking,
	"agkn.com":              CategoryTracking,
	"rlcdn.com":             CategoryTracking,
	"matomo.cloud":          CategoryTracking,
	"hubspot.com":           CategoryTracking,
	"marketo.net":           CategoryTracking,
	"pardot.com":            CategoryTracking,

	// Telemetry
	"app-measurement.com": CategoryTelemetry,
	"crashlytics.com":     CategoryTelemetry,
	"bugsnag.com":         CategoryTelemetry,
	"sentry.io":           CategoryTelemetry,
	"newrelic.com":        CategoryTelemetry,
	"nr-data.net":         CategoryTelemetry,
	"datadoghq.com":       CategoryTelemetry,
}

// adtechASNs là các ASN mà gần như toàn bộ địa chỉ phục vụ adtech. Danh sách này
// phải giữ hẹp: xem neutralASNs bên dưới để hiểu vì sao.
var adtechASNs = map[int]adtechASN{
	62597:  {"Adform", CategoryAds},
	394699: {"Criteo", CategoryAds},
	55569:  {"PubMatic", CategoryAds},
	32787:  {"Prolexic/Akamai Ads", CategoryAds},
	29990:  {"AppNexus", CategoryAds},
	36459:  {"GitHub Ads", CategoryAds},
	26667:  {"OpenX", CategoryAds},
	205016: {"Taboola", CategoryAds},
	16652:  {"Outbrain", CategoryAds},
	14413:  {"LinkedIn Ads", CategoryTracking},
	40977:  {"Quantcast", CategoryTracking},
	54994:  {"Segment", CategoryTracking},
	397165: {"Amplitude", CategoryTracking},
}

type adtechASN struct {
	Org      string
	Category Category
}

// neutralASNs là các ASN chứa lẫn lộn cả hạ tầng adtech lẫn hạ tầng bình thường.
// Google (15169) chứa cả doubleclick lẫn google.com; Cloudflare (13335) chứa gần
// như mọi thứ. Coi chúng là adtech sẽ chặn nửa Internet, nên tín hiệu asn_adtech
// không bao giờ kích hoạt trong các ASN này.
var neutralASNs = map[int]string{
	15169: "Google",
	13335: "Cloudflare",
	16509: "Amazon",
	14618: "Amazon",
	8075:  "Microsoft",
	32934: "Meta",
	714:   "Apple",
	54113: "Fastly",
	20940: "Akamai",
	16625: "Akamai",
	13414: "Twitter",
	2906:  "Netflix",
	46489: "Twitch",
}

// sharedCDNSuffixes là hạ tầng CDN dùng chung. Domain phân giải về đây phục vụ cả
// nội dung lẫn quảng cáo, nên chặn theo tên miền là chặn nhầm hàng loạt trang
// không liên quan. Tín hiệu shared_cdn mang trọng số âm lớn vì lý do đó.
var sharedCDNSuffixes = []string{
	"akamai.net", "akamaiedge.net", "akamaized.net", "edgekey.net", "edgesuite.net",
	"cloudfront.net", "fastly.net", "fastlylb.net", "cdn.cloudflare.net",
	"gstatic.com", "googleapis.com", "googleusercontent.com",
	"azureedge.net", "azurefd.net", "cloudflare.com",
	"bunnycdn.com", "b-cdn.net", "jsdelivr.net", "unpkg.com",
}

// keywordGroups gom từ khóa theo phân loại mà chúng gợi ý. Việc gom nhóm phục vụ
// bước gán nhãn: một domain khớp "analytic" là dấu hiệu tracking, khớp "adserv"
// là dấu hiệu ads. Khớp theo chuỗi con, nên "metric" bắt được cả "metrics".
var keywordGroups = map[Category][]string{
	CategoryAds: {
		"adserv", "adservice", "adsystem", "adnxs", "doubleclick", "adtech",
		"advert", "banner", "popads", "pubads", "adroll", "adcolony",
		"clickserv", "impression", "rtb", "bidder", "prebid", "dsp", "ssp",
		"cdn-ads", "affiliate", "retarget",
	},
	CategoryTracking: {
		"analytic", "metric", "tracking", "tracker", "beacon", "pixel", "collect",
	},
	CategoryTelemetry: {
		"telemetry",
	},
}

// hasAdtechSuffix cho biết host có nằm dưới một domain adtech đã biết không, và
// trả về phân loại của domain đó. Khớp theo hậu tố ở ranh giới nhãn: "eulerian.net"
// bắt "x.eulerian.net" nhưng không bắt "noteulerian.net".
func hasAdtechSuffix(host string) (Category, string, bool) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for suffix, cat := range adtechDomains {
		if matchesSuffix(host, suffix) {
			return cat, suffix, true
		}
	}
	return "", "", false
}

// isSharedCDN cho biết host có thuộc hạ tầng CDN dùng chung không.
func isSharedCDN(host string) (string, bool) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, suffix := range sharedCDNSuffixes {
		if matchesSuffix(host, suffix) {
			return suffix, true
		}
	}
	return "", false
}

// matchesSuffix khớp hậu tố ở ranh giới nhãn, không phải khớp chuỗi con.
func matchesSuffix(host, suffix string) bool {
	if host == suffix {
		return true
	}
	return strings.HasSuffix(host, "."+suffix)
}
