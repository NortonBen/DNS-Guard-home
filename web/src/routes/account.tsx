import { useState, type FormEvent } from 'react';

import { ApiError } from '@/api/client';
import { useChangePassword, useSession } from '@/api/hooks';
import { Field, TextInput } from '@/components/ui/form';
import { Button, Card, ErrorState, Spinner } from '@/components/ui/primitives';

/**
 * Màn Tài khoản.
 *
 * Tách khỏi màn Cài đặt vì Cài đặt chỉ dành cho quản trị viên, còn đổi mật khẩu thì
 * mọi vai trò đều phải làm được — kể cả tài khoản chỉ đọc, nếu không mật khẩu do
 * quản trị viên đặt sẽ nằm nguyên đó mãi mãi.
 */
export function AccountScreen() {
  const session = useSession();

  if (session.isPending) return <Spinner label="Đang tải tài khoản" />;
  if (session.isError) return <ErrorState error={session.error} />;
  if (!session.data) return null;

  return (
    <div className="space-y-3">
      <Card title="Tài khoản">
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
          <dt className="text-slate-500 dark:text-slate-400">Tên đăng nhập</dt>
          <dd className="font-medium">{session.data.user.username}</dd>
          <dt className="text-slate-500 dark:text-slate-400">Vai trò</dt>
          <dd>{session.data.user.role === 'admin' ? 'Quản trị' : 'Chỉ đọc'}</dd>
        </dl>
      </Card>

      <PasswordSection />
    </div>
  );
}

/**
 * Ô ghi chú của Field vẽ bằng màu xám nhạt — đúng cho gợi ý, sai cho lỗi. Bọc lại để
 * thông báo lỗi đọc ra là lỗi chứ không lẫn với hướng dẫn.
 */
function fieldError(message: string | null | undefined) {
  if (!message) return undefined;
  return <span className="text-red-600 dark:text-red-400">{message}</span>;
}

/** Kết quả một lần đổi mật khẩu thành công, để hiện thông báo rồi thôi. */
interface Done {
  revoked: number;
}

function PasswordSection() {
  const change = useChangePassword();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [done, setDone] = useState<Done | null>(null);

  // Chỉ kiểm tra ở phía trình duyệt đúng thứ mà máy chủ không kiểm được: ô xác nhận
  // không bao giờ được gửi đi. Độ dài và trùng mật khẩu cũ để máy chủ phán, tránh hai
  // nơi giữ hai bản chính sách rồi lệch nhau.
  const mismatch = confirm !== '' && next !== confirm;

  /**
   * Xóa kết quả của lần thử trước ngay khi người dùng gõ tiếp.
   *
   * Nếu không, dòng "đã đổi mật khẩu" của lần trước vẫn nằm đó trong lúc người dùng
   * điền lần mới — trông y như lần này vừa thành công.
   */
  function edit(set: (value: string) => void) {
    return (value: string) => {
      if (done) setDone(null);
      if (change.isError) change.reset();
      set(value);
    };
  }

  const editCurrent = edit(setCurrent);
  const editNext = edit(setNext);
  const editConfirm = edit(setConfirm);
  const canSubmit = current !== '' && next !== '' && !mismatch && !change.isPending;

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;

    change.mutate(
      { current_password: current, new_password: next },
      {
        onSuccess: (data) => {
          setDone({ revoked: data.revoked_sessions });
          setCurrent('');
          setNext('');
          setConfirm('');
        },
      },
    );
  }

  // Máy chủ tách mã lỗi theo ô nhập, nên thông báo bám đúng ô gây lỗi thay vì treo
  // một dòng đỏ chung chung ở cuối biểu mẫu.
  const error = change.error instanceof ApiError ? change.error : null;
  const currentError = error?.code === 'invalid_password' ? error.message : null;
  const policyError = error?.code === 'password_policy' ? error.message : null;

  return (
    <Card title="Đổi mật khẩu">
      <form onSubmit={handleSubmit} className="max-w-sm space-y-3">
        <Field label="Mật khẩu hiện tại" htmlFor="current-password" required hint={fieldError(currentError)}>
          <TextInput
            id="current-password"
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => editCurrent(e.target.value)}
            aria-invalid={currentError ? true : undefined}
          />
        </Field>

        <Field label="Mật khẩu mới" htmlFor="new-password" required hint={fieldError(policyError)}>
          <TextInput
            id="new-password"
            type="password"
            autoComplete="new-password"
            required
            value={next}
            onChange={(e) => editNext(e.target.value)}
            aria-invalid={policyError ? true : undefined}
          />
        </Field>

        <Field
          label="Nhập lại mật khẩu mới"
          htmlFor="confirm-password"
          required
          hint={mismatch ? fieldError('Hai lần nhập không khớp') : undefined}
        >
          <TextInput
            id="confirm-password"
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => editConfirm(e.target.value)}
            aria-invalid={mismatch ? true : undefined}
          />
        </Field>

        {/* Lỗi không gắn được vào ô nào, ví dụ bị chặn vì thử sai quá nhiều lần. */}
        {error && !currentError && !policyError && <ErrorState error={error} />}

        {done && (
          <p role="status" className="text-sm text-emerald-700 dark:text-emerald-400">
            Đã đổi mật khẩu.{' '}
            {done.revoked > 0
              ? `${done.revoked} phiên khác đã bị đăng xuất.`
              : 'Không có phiên nào khác đang mở.'}
          </p>
        )}

        <Button type="submit" variant="primary" disabled={!canSubmit}>
          {change.isPending ? 'Đang đổi…' : 'Đổi mật khẩu'}
        </Button>
      </form>
    </Card>
  );
}
