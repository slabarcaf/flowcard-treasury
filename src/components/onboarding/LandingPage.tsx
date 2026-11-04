interface Props {
  onGetStarted: () => void;
  onLogin: () => void;
}

export function LandingPage({ onGetStarted, onLogin }: Props) {
  return (
    <div className="hero-page">
      {/* Card scene artwork — positioned right, fades into background */}
      <div className="hero-artwork" role="presentation">
        <img
          src="/hero-scene.png"
          alt=""
          className="hero-artwork-img"
          draggable={false}
        />
        {/* Left-edge blend so image dissolves into the dark bg */}
        <div className="hero-artwork-fade" />
      </div>

      {/* Nav */}
      <header className="hero-nav">
        <span className="hero-wordmark">FlowCard</span>
        <span className="hero-nav-login" onClick={onLogin}>Log in</span>
      </header>

      {/* Hero copy */}
      <main className="hero-main">
        <div className="hero-copy">
          <h1 className="hero-headline">
            Go beyond<br />
            treasury as usual
          </h1>
          <p className="hero-sub">
            Take your finances further with the platform designed<br className="hero-br-desktop" />
            for efficiency, and built for business.
          </p>
          <button onClick={onGetStarted} className="hero-cta">
            Get started
          </button>
        </div>
      </main>
    </div>
  );
}
