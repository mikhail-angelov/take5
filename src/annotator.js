/**
 * Injects highlight ring + tooltip into the live page DOM.
 * These render inside the Playwright recordVideo capture.
 */
export async function injectAnnotation(
  page,
  description,
  { selector = null, targetRect = null } = {},
) {
  await page.evaluate(
    ({ description, selector, targetRect }) => {
      // Remove existing annotations immediately (no fade out)
      document.querySelectorAll('.__t5').forEach(el => el.remove());

      const tip = document.createElement('div');
      tip.className = '__t5';
      tip.textContent = description;
      Object.assign(tip.style, {
        position: 'fixed',
        background: '#FF5C00', // Orange background
        color: '#fff',
        padding: '12px 20px', // Bigger padding
        borderRadius: '10px',
        fontSize: '18px', // Bigger font
        fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        fontWeight: '600', // Bolder
        zIndex: '2147483647',
        pointerEvents: 'none',
        opacity: '1', // Immediate appearance
        boxShadow: '0 4px 20px rgba(0,0,0,0.4), 0 0 0 2px #fff', // White outline effect
        maxWidth: '420px',
        lineHeight: '1.4',
        left: '24px',
        bottom: '32px',
        border: '2px solid #fff', // White border for better visibility
        textShadow: '1px 1px 2px rgba(0,0,0,0.5)', // Text shadow for readability
      });

      let rect = null;
      if (targetRect) {
        rect = {
          left: targetRect.x,
          top: targetRect.y,
          width: targetRect.width,
          height: targetRect.height,
          bottom: targetRect.y + targetRect.height,
        };
      } else if (selector) {
        const el = document.querySelector(selector);
        if (el) {
          rect = el.getBoundingClientRect();
        }
      }

      if (rect) {
        const r = rect;

        const ring = document.createElement('div');
        ring.className = '__t5';
        Object.assign(ring.style, {
          position: 'fixed',
          border: '4px solid #FF5C00', // Thicker border
          borderRadius: '10px',
          left: `${r.left - 8}px`, // Larger offset
          top: `${r.top - 8}px`,
          width: `${r.width + 16}px`,
          height: `${r.height + 16}px`,
          zIndex: '2147483646',
          pointerEvents: 'none',
          opacity: '1', // Immediate appearance
          boxShadow: '0 0 0 4px rgba(255,92,0,0.3), 0 0 20px rgba(255,92,0,0.5)', // More prominent glow
          animation: 'pulse 1.5s infinite', // Add pulsing animation
        });

        // Add pulsing animation CSS
        if (!document.querySelector('#t5-pulse-animation')) {
          const style = document.createElement('style');
          style.id = 't5-pulse-animation';
          style.textContent = `
            @keyframes pulse {
              0% { box-shadow: 0 0 0 4px rgba(255,92,0,0.3), 0 0 20px rgba(255,92,0,0.5); }
              50% { box-shadow: 0 0 0 8px rgba(255,92,0,0.2), 0 0 30px rgba(255,92,0,0.6); }
              100% { box-shadow: 0 0 0 4px rgba(255,92,0,0.3), 0 0 20px rgba(255,92,0,0.5); }
            }
          `;
          document.head.appendChild(style);
        }

        document.body.appendChild(ring);

        // Position tooltip above element if space allows
        const tipTop = r.top - 65; // More space for bigger tooltip
        if (tipTop > 8) {
          tip.style.bottom = 'auto';
          tip.style.left = `${Math.max(8, Math.min(r.left, window.innerWidth - 450))}px`;
          tip.style.top = `${tipTop}px`;
        } else {
          // If not enough space above, position below
          tip.style.bottom = 'auto';
          tip.style.top = `${r.bottom + 12}px`;
          tip.style.left = `${Math.max(8, Math.min(r.left, window.innerWidth - 450))}px`;
        }
      }

      document.body.appendChild(tip);
    },
    { description, selector, targetRect }
  );
}

export async function clearAnnotation(page) {
  await page.evaluate(() => {
    // Remove annotations immediately (no fade out)
    document.querySelectorAll('.__t5').forEach(el => el.remove());
  });
}

/**
 * Show "the end" annotation with confetti animation
 */
export async function showEndAnnotation(page) {
  await page.evaluate(() => {
    // Remove existing annotations
    document.querySelectorAll('.__t5').forEach(el => el.remove());
    
    // Create "the end" annotation
    const endTip = document.createElement('div');
    endTip.className = '__t5';
    endTip.textContent = '🎉 The End!';
    Object.assign(endTip.style, {
      position: 'fixed',
      background: '#FF5C00',
      color: '#fff',
      padding: '20px 30px',
      borderRadius: '15px',
      fontSize: '28px',
      fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
      fontWeight: '700',
      zIndex: '2147483647',
      pointerEvents: 'none',
      opacity: '1',
      boxShadow: '0 8px 30px rgba(0,0,0,0.4), 0 0 0 3px #fff',
      maxWidth: '500px',
      lineHeight: '1.4',
      left: '50%',
      top: '50%',
      transform: 'translate(-50%, -50%)',
      border: '3px solid #fff',
      textShadow: '2px 2px 4px rgba(0,0,0,0.5)',
      textAlign: 'center',
      animation: 'pulse-end 2s infinite',
    });
    
    // Add confetti animation
    const confettiContainer = document.createElement('div');
    confettiContainer.className = '__t5';
    confettiContainer.id = 't5-confetti';
    Object.assign(confettiContainer.style, {
      position: 'fixed',
      top: '0',
      left: '0',
      width: '100%',
      height: '100%',
      pointerEvents: 'none',
      zIndex: '2147483646',
      overflow: 'hidden',
    });
    
    // Add confetti animation CSS
    if (!document.querySelector('#t5-confetti-animation')) {
      const style = document.createElement('style');
      style.id = 't5-confetti-animation';
      style.textContent = `
        @keyframes pulse-end {
          0% { transform: translate(-50%, -50%) scale(1); }
          50% { transform: translate(-50%, -50%) scale(1.05); }
          100% { transform: translate(-50%, -50%) scale(1); }
        }
        
        @keyframes confetti-fall {
          0% { transform: translateY(-100px) rotate(0deg); opacity: 1; }
          100% { transform: translateY(100vh) rotate(360deg); opacity: 0; }
        }
        
        .t5-confetti-piece {
          position: absolute;
          width: 10px;
          height: 10px;
          background: #FF5C00;
          top: -10px;
          opacity: 0;
        }
        
        .t5-confetti-piece:nth-child(5n) { background: #FFD700; }
        .t5-confetti-piece:nth-child(5n+1) { background: #FF1493; }
        .t5-confetti-piece:nth-child(5n+2) { background: #00FF00; }
        .t5-confetti-piece:nth-child(5n+3) { background: #00BFFF; }
        .t5-confetti-piece:nth-child(5n+4) { background: #9400D3; }
      `;
      document.head.appendChild(style);
    }
    
    // Create confetti pieces
    for (let i = 0; i < 150; i++) {
      const confetti = document.createElement('div');
      confetti.className = 't5-confetti-piece';
      Object.assign(confetti.style, {
        left: `${Math.random() * 100}%`,
        animation: `confetti-fall ${2 + Math.random() * 3}s linear ${Math.random() * 2}s forwards`,
        borderRadius: Math.random() > 0.5 ? '50%' : '0',
        width: `${5 + Math.random() * 10}px`,
        height: `${5 + Math.random() * 10}px`,
        transform: `rotate(${Math.random() * 360}deg)`,
      });
      confettiContainer.appendChild(confetti);
    }
    
    document.body.appendChild(endTip);
    document.body.appendChild(confettiContainer);
  });
}
