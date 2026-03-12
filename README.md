# llm-token-gateway
A transparent reverse proxy that reduces LLM API costs by optimizing token usage across all your AI coding agents.

  
<svg width="100%" viewBox="0 0 680 520">
<defs>
  <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
    <path d="M2 1L8 5L2 9" fill="none" stroke="context-stroke" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
  </marker>
</defs>

<!-- LLM Agents tier -->
<text class="th" x="40" y="30">LLM agents</text>
<g class="node c-gray" onclick="sendPrompt('How should Claude Code be configured to route through the gateway?')">
  <rect x="40" y="44" width="130" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="105" y="66" text-anchor="middle" dominant-baseline="central">Claude Code</text>
</g>
<g class="node c-gray" onclick="sendPrompt('How to configure Cursor IDE to use the gateway?')">
  <rect x="190" y="44" width="110" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="245" y="66" text-anchor="middle" dominant-baseline="central">Cursor</text>
</g>
<g class="node c-gray" onclick="sendPrompt('How to route custom agents through the gateway?')">
  <rect x="320" y="44" width="140" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="390" y="66" text-anchor="middle" dominant-baseline="central">Custom agents</text>
</g>
<g class="node c-gray" onclick="sendPrompt('How to integrate Aider with the gateway?')">
  <rect x="480" y="44" width="100" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="530" y="66" text-anchor="middle" dominant-baseline="central">Aider</text>
</g>

<!-- Arrows down to gateway -->
<line x1="105" y1="88" x2="280" y2="140" class="arr" marker-end="url(#arrow)"/>
<line x1="245" y1="88" x2="310" y2="140" class="arr" marker-end="url(#arrow)"/>
<line x1="390" y1="88" x2="370" y2="140" class="arr" marker-end="url(#arrow)"/>
<line x1="530" y1="88" x2="400" y2="140" class="arr" marker-end="url(#arrow)"/>

<!-- Gateway container -->
<g class="c-purple">
  <rect x="60" y="140" width="560" height="220" rx="20" stroke-width="0.5"/>
  <text class="th" x="340" y="168" text-anchor="middle">Token optimization gateway</text>
</g>

<!-- Internal: Request pipeline -->
<g class="c-teal">
  <rect x="90" y="186" width="150" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="165" y="206" text-anchor="middle" dominant-baseline="central">Request interceptor</text>
  <text class="ts" x="165" y="224" text-anchor="middle" dominant-baseline="central">Parse + classify</text>
</g>

<!-- Internal: Token optimizer -->
<g class="c-coral">
  <rect x="265" y="186" width="150" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="340" y="206" text-anchor="middle" dominant-baseline="central">Token optimizer</text>
  <text class="ts" x="340" y="224" text-anchor="middle" dominant-baseline="central">JSON → TOON</text>
</g>

<!-- Internal: Response handler -->
<g class="c-teal">
  <rect x="440" y="186" width="150" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="515" y="206" text-anchor="middle" dominant-baseline="central">Response handler</text>
  <text class="ts" x="515" y="224" text-anchor="middle" dominant-baseline="central">Stream passthrough</text>
</g>

<!-- Arrows between internal components -->
<line x1="240" y1="214" x2="263" y2="214" class="arr" marker-end="url(#arrow)"/>
<line x1="415" y1="214" x2="438" y2="214" class="arr" marker-end="url(#arrow)"/>

<!-- Bottom row inside gateway -->
<g class="c-amber">
  <rect x="90" y="268" width="130" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="155" y="288" text-anchor="middle" dominant-baseline="central">Prompt cache</text>
  <text class="ts" x="155" y="306" text-anchor="middle" dominant-baseline="central">Hash-based dedup</text>
</g>

<g class="c-amber">
  <rect x="245" y="268" width="150" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="320" y="288" text-anchor="middle" dominant-baseline="central">Schema registry</text>
  <text class="ts" x="320" y="306" text-anchor="middle" dominant-baseline="central">TOON encoding hints</text>
</g>

<g class="c-amber">
  <rect x="420" y="268" width="170" height="56" rx="8" stroke-width="0.5"/>
  <text class="th" x="505" y="288" text-anchor="middle" dominant-baseline="central">Metrics collector</text>
  <text class="ts" x="505" y="306" text-anchor="middle" dominant-baseline="central">Tokens saved, costs</text>
</g>

<!-- Dashed lines connecting bottom to top row -->
<line class="leader" x1="155" y1="268" x2="165" y2="242"/>
<line class="leader" x1="320" y1="268" x2="340" y2="242"/>
<line class="leader" x1="505" y1="268" x2="515" y2="242"/>

<!-- Arrows down to providers -->
<line x1="200" y1="360" x2="120" y2="410" class="arr" marker-end="url(#arrow)"/>
<line x1="340" y1="360" x2="310" y2="410" class="arr" marker-end="url(#arrow)"/>
<line x1="480" y1="360" x2="500" y2="410" class="arr" marker-end="url(#arrow)"/>

<!-- Provider tier -->
<text class="th" x="40" y="400">LLM providers</text>
<g class="node c-blue" onclick="sendPrompt('How does the gateway handle Anthropic API specifics?')">
  <rect x="40" y="410" width="160" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="120" y="432" text-anchor="middle" dominant-baseline="central">Anthropic API</text>
</g>
<g class="node c-blue" onclick="sendPrompt('How does the gateway handle OpenAI API routing?')">
  <rect x="220" y="410" width="160" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="300" y="432" text-anchor="middle" dominant-baseline="central">OpenAI API</text>
</g>
<g class="node c-blue" onclick="sendPrompt('How does the gateway handle Google Gemini API?')">
  <rect x="420" y="410" width="160" height="44" rx="8" stroke-width="0.5"/>
  <text class="th" x="500" y="432" text-anchor="middle" dominant-baseline="central">Google Gemini</text>
</g>

<!-- Cost savings callout -->
<g class="c-green">
  <rect x="200" y="474" width="280" height="36" rx="18" stroke-width="0.5"/>
  <text class="th" x="340" y="492" text-anchor="middle" dominant-baseline="central">~40% input token savings on structured data</text>
</g>
</svg>
