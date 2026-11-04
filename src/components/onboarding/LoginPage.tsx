import { useState } from 'react';
import { AuroraBackground } from './AuroraBackground.js';

interface Props {
  onLogin: () => void;
  onBack: () => void;
}

export function LoginPage({ onLogin, onBack }: Props) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState('');

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');

    if (!email.trim() || !password.trim()) {
      setError('Please enter your email and password');
      return;
    }

    onLogin();
  }

  return (
    <div className="min-h-screen flex flex-col relative" style={{ backgroundColor: '#0D1421' }}>
      <AuroraBackground />

      <main className="flex-1 flex flex-col items-center justify-center px-4 relative z-10">
        <div className="w-full max-w-[400px]">
          {/* Back */}
          <button
            type="button"
            onClick={onBack}
            className="mb-8 text-white/70 hover:text-white transition-colors cursor-pointer"
            aria-label="Go back"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M19 12H5" />
              <polyline points="12 19 5 12 12 5" />
            </svg>
          </button>

          <h1 className="text-2xl font-bold mb-2 text-white">Welcome back</h1>
          <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>Log in to your FlowCard account</p>

          <form onSubmit={handleSubmit} className="space-y-4">
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="Email address"
              className="revolut-input w-full"
            />

            <div className="relative">
              <input
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Password"
                className="revolut-input w-full pr-11"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 cursor-pointer"
                style={{ color: '#5A6577' }}
                tabIndex={-1}
              >
                {showPassword ? (
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
                    <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
                    <line x1="1" y1="1" x2="23" y2="23" />
                  </svg>
                ) : (
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                    <circle cx="12" cy="12" r="3" />
                  </svg>
                )}
              </button>
            </div>

            {error && (
              <p className="text-xs" style={{ color: '#E34850' }}>{error}</p>
            )}

            <div className="pt-4">
              <button type="submit" className="revolut-btn-primary w-full">
                Log in
              </button>
            </div>
          </form>
        </div>
      </main>
    </div>
  );
}
