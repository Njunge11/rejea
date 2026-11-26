"""One chunk, the same instruction, three request forms through the OpenAI-compatible endpoint: read the output tokens of each."""
import json, os, sys, time
from openai import OpenAI
client = OpenAI(base_url="https://api.anthropic.com/v1", api_key=os.environ["CHAT_KEY"])
chunks = json.load(open(sys.argv[1])); text = sorted(chunks, key=lambda c: abs(len(c["text"].split()) - 150))[0]["text"]
messages = [{"role": "system", "content": "Extract the facts of the passage as a JSON list of [subject, relation, object] triples. Answer with the JSON only."}, {"role": "user", "content": text}]
forms = {"no setting": {}, "output_config effort low": {"output_config": {"effort": "low"}}, "thinking disabled": {"thinking": {"type": "disabled"}}}
for name, extra in forms.items():
    start = time.time()
    try:
        r = client.chat.completions.create(model="claude-haiku-5-5", messages=messages, max_completion_tokens=4000, extra_body=extra or None)
        print(f"{name:26} | {time.time() - start:5.1f} s | output tokens {r.usage.completion_tokens:5d} | answer characters {len(r.choices[0].message.content or ''):5d} | input tokens {r.usage.prompt_tokens}")
    except Exception as e: print(f"{name:26} | error: {str(e)[:260]}")
