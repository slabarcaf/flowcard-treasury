function DashboardIcon() {
  return <svg className="w-5 h-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" /></svg>;
}

export function Sidebar() {
  return (
    <aside className="w-60 shrink-0 bg-[#1a1b2e] flex flex-col min-h-screen">
      {/* Logo / Org */}
      <div className="px-5 py-5 flex items-center gap-3">
        <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-orange-400 to-rose-500 flex items-center justify-center">
          <span className="text-white text-xs font-bold">FC</span>
        </div>
        <div className="min-w-0">
          <div className="text-sm font-semibold text-white truncate">FlowCard</div>
          <div className="text-[11px] text-gray-400">Treasury</div>
        </div>
      </div>

      {/* Nav: the dashboard is the only section */}
      <nav className="flex-1 px-3 py-2">
        <div className="w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-orange-400 bg-white/5">
          <DashboardIcon />
          <span className="font-medium">Dashboard</span>
        </div>
      </nav>
    </aside>
  );
}
