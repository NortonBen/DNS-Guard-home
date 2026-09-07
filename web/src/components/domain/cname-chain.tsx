import { cx } from '@/components/ui/primitives';

/**
 * Tập hạ tầng adtech đã biết, dùng để đánh dấu mắt xích đáng ngờ trong chuỗi.
 *
 * Chỉ để tô màu ở giao diện; quyết định chấm điểm nằm ở backend. Danh sách rút gọn
 * là đủ: mục đích là làm nổi bật mắt xích, không phải phân loại lại.
 */
const adtechHints = [
  'doubleclick.net', 'googlesyndication.com', 'googleadservices.com', 'adnxs.com',
  'adsrvr.org', 'criteo.com', 'criteo.net', 'adform.net', 'eulerian.net',
  'scorecardresearch.com', 'quantserve.com', 'demdex.net', 'omtrdc.net',
  'krxd.net', 'rlcdn.com', 'appsflyer.com', 'adjust.com', 'branch.io',
  'taboola.com', 'outbrain.com', 'pubmatic.com', 'openx.net', 'rubiconproject.com',
  'admicro.vn', 'adtima.vn', 'eclick.vn',
];

function isAdtech(host: string): boolean {
  const name = host.toLowerCase().replace(/\.$/, '');
  return adtechHints.some((suffix) => name === suffix || name.endsWith(`.${suffix}`));
}

/**
 * Chuỗi CNAME dạng đồ thị một chiều.
 *
 * Đây là bằng chứng quan trọng nhất trên trang chi tiết: một tên miền trông như của
 * chính trang chủ nhà nhưng CNAME dẫn về hạ tầng quảng cáo là dấu hiệu gần như chắc
 * chắn. Mắt xích thuộc adtech được đánh dấu rõ.
 */
export function CnameChain({ domain, chain }: { domain: string; chain: string[] | undefined }) {
  if (!chain || chain.length === 0) {
    return (
      <p className="text-sm text-slate-500 dark:text-slate-400">
        Không có bản ghi CNAME — domain phân giải trực tiếp
      </p>
    );
  }

  const hops = [domain, ...chain];

  return (
    <ol className="flex flex-wrap items-center gap-1.5">
      {hops.map((hop, index) => {
        const flagged = index > 0 && isAdtech(hop);
        return (
          <li key={`${hop}-${index}`} className="flex items-center gap-1.5">
            {index > 0 && (
              <span aria-hidden className="text-slate-400 dark:text-slate-600">
                →
              </span>
            )}
            <span
              className={cx(
                'domain-name rounded px-1.5 py-0.5',
                flagged
                  ? 'bg-red-50 text-red-700 ring-1 ring-red-200 dark:bg-red-950 dark:text-red-300 dark:ring-red-900'
                  : 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
              )}
            >
              {hop}
              {flagged && (
                <span className="ml-1.5 text-[10px] font-semibold uppercase tracking-wide">
                  adtech
                </span>
              )}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
