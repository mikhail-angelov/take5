const TOOLBAR_FRAME_ID = 'take5-toolbar-frame';
const TOOLBAR_LAUNCHER_ID = 'take5-toolbar-launcher';
const TOGGLE_MESSAGE_TYPE = 'take5:toolbar-toggle';

function injectToolbar() {
  if (document.getElementById(TOOLBAR_FRAME_ID) || document.getElementById(TOOLBAR_LAUNCHER_ID)) {
    return;
  }

  const launcher = document.createElement('button');
  launcher.id = TOOLBAR_LAUNCHER_ID;
  launcher.type = 'button';
  launcher.textContent = 'Take5';
  launcher.setAttribute('aria-label', 'Show Take5 toolbar');
  launcher.style.position = 'fixed';
  launcher.style.right = '18px';
  launcher.style.bottom = '18px';
  launcher.style.zIndex = '2147483647';
  launcher.style.display = 'none';
  launcher.style.border = '0';
  launcher.style.borderRadius = '999px';
  launcher.style.padding = '10px 14px';
  launcher.style.background = 'linear-gradient(135deg, #38bdf8, #0ea5e9)';
  launcher.style.color = '#04111f';
  launcher.style.font = '600 13px/1.1 system-ui, sans-serif';
  launcher.style.boxShadow = '0 14px 40px rgba(2, 6, 23, 0.3)';
  launcher.style.cursor = 'pointer';

  const frame = document.createElement('iframe');
  frame.id = TOOLBAR_FRAME_ID;
  frame.title = 'Take5 toolbar';
  frame.src = chrome.runtime.getURL('toolbar.html');
  frame.setAttribute('aria-label', 'Take5 toolbar');
  frame.style.position = 'fixed';
  frame.style.right = '16px';
  frame.style.bottom = '16px';
  frame.style.width = '360px';
  frame.style.height = '560px';
  frame.style.border = '0';
  frame.style.zIndex = '2147483647';
  frame.style.background = 'transparent';
  frame.style.boxShadow = '0 18px 50px rgba(15, 23, 42, 0.28)';
  frame.style.borderRadius = '18px';
  frame.style.overflow = 'hidden';

  launcher.addEventListener('click', () => {
    frame.style.display = 'block';
    launcher.style.display = 'none';
  });

  window.addEventListener('message', (event) => {
    if (event.source !== frame.contentWindow || event.data?.type !== TOGGLE_MESSAGE_TYPE) {
      return;
    }

    frame.style.display = 'none';
    launcher.style.display = 'block';
  });

  const root = document.body || document.documentElement;
  root.appendChild(launcher);
  root.appendChild(frame);
}

injectToolbar();
