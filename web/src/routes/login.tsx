import { useState, type FormEvent } from 'react';
import { useNavigate } from '@tanstack/react-router';

import { setCsrfToken } from '@/api/client';
import { useLogin } from '@/api/hooks';
import { Logo } from '@/components/ui/logo';
import { Button, ErrorState } from '@/components/ui/primitives';

export function LoginScreen() {
  const navigate = useNavigate();
  const login = useLogin();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    login.mutate(
      { username, password },
      {
        onSuccess: (data) => {
          setCsrfToken(data.csrf_token);
          navigate({ to: '/' });
        },
      },
    );
  }

  return (
    <div className="grid min-h-dvh place-items-center px-4">
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-sm space-y-4 rounded-lg bg-white p-6 ring-1 ring-slate-200 dark:bg-slate-900 dark:ring-slate-800"
      >
        <div>
          <h1>
            <Logo className="text-xl" />
          </h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Quản trị blocklist DNS lấy domain làm trung tâm
          </p>
        </div>

        <div className="space-y-1">
          <label htmlFor="username" className="block text-sm font-medium">
            Tài khoản
          </label>
          <input
            id="username"
            name="username"
            autoComplete="username"
            required
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="w-full rounded-md px-3 py-2 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
        </div>

        <div className="space-y-1">
          <label htmlFor="password" className="block text-sm font-medium">
            Mật khẩu
          </label>
          <input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md px-3 py-2 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
        </div>

        {login.isError && <ErrorState error={login.error} />}

        <Button type="submit" variant="primary" disabled={login.isPending} className="w-full">
          {login.isPending ? 'Đang đăng nhập…' : 'Đăng nhập'}
        </Button>
      </form>
    </div>
  );
}
