/**
 * Chuỗi giao diện gom về một chỗ.
 *
 * Chưa dùng thư viện i18n vì hệ thống chỉ phục vụ một mạng và một ngôn ngữ; gom sẵn
 * ở đây để khi cần thêm ngôn ngữ thì không phải đi lùng chuỗi trong từng component.
 */

import type { DomainStatus, DecisionAction } from '@/api/types';

export const statusLabels: Record<DomainStatus, string> = {
  new: 'Mới',
  staging: 'Chờ duyệt',
  blocked: 'Đã chặn',
  allowed: 'Cho qua',
  ignored: 'Bỏ qua',
};

/**
 * Màu trạng thái. Đỏ cho chặn, vàng cho chờ duyệt, xanh cho cho qua, xám cho mới —
 * giống quy ước màu của đồ thị quan hệ để hai nơi đọc được như nhau.
 */
export const statusColors: Record<DomainStatus, string> = {
  new: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  staging: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
  blocked: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-300',
  allowed: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300',
  ignored: 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400',
};

export const actionLabels: Record<DecisionAction, string> = {
  block: 'Chặn',
  allow: 'Cho qua',
  ignore: 'Bỏ qua',
  recategorize: 'Đổi phân loại',
  stage: 'Vào hàng chờ',
  expire: 'Hết hạn',
};

export const originLabels: Record<string, string> = {
  discovered: 'Phát hiện trong mạng',
  manual: 'Thêm thủ công',
  public_list: 'Từ danh sách công khai',
  related: 'Domain liên quan',
};

export const relationLabels: Record<string, string> = {
  cname_to: 'CNAME tới',
  same_asn: 'Cùng ASN',
  same_cert: 'Cùng chứng chỉ',
  co_occurs: 'Đồng xuất hiện',
};

export const sourceStatusLabels: Record<string, string> = {
  ok: 'Bình thường',
  not_modified: 'Không đổi',
  http_error: 'Lỗi HTTP',
  network_error: 'Lỗi mạng',
  parse_error: 'Lỗi phân tích',
  decode_error: 'Lỗi giải nén',
  invalid_url: 'URL không hợp lệ',
  suspicious_drop: 'Sụt giảm bất thường',
};
