import http from "node:http"
import path from "node:path"
import { loadModel, translate } from "@qvac/sdk"

const root = process.env.QVAC_TRANSLATION_ROOT
const variant = process.env.QVAC_TRANSLATION_VARIANT || "Tiny"
const port = Number(process.env.PORT || 8104)
const supported = new Set(["en", "de", "es", "fr", "it", "pt", "fi", "cs", "nl", "sv"])
const tags = { de: "##DE", es: "##ES", fr: "##FR", it: "##IT", pt: "##PT", fi: "##FI", cs: "##CS", nl: "##NL", sv: "##SV" }
const models = new Map()

function modelDir(direction) {
  return path.join(root, direction, variant, "intgemm")
}

async function getModel(direction, from, to) {
  const key = `${direction}:${from}:${to}`
  if (models.has(key)) return models.get(key)
  const dir = modelDir(direction)
  const modelId = await loadModel({
    modelSrc: path.join(dir, "model.intgemm.alphas.bin"),
    modelType: "nmt",
    modelConfig: {
      engine: "Bergamot",
      from,
      to,
      srcVocabSrc: path.join(dir, "vocab.spm"),
      dstVocabSrc: path.join(dir, "vocab.spm")
    }
  })
  models.set(key, modelId)
  return modelId
}

async function runModel(direction, from, to, text) {
  const modelId = await getModel(direction, from, to)
  const input = direction === "en-xx" ? `${tags[to]} ${text}` : text
  const { text: output } = translate({ modelId, text: input, modelType: "nmt", stream: false })
  return (await output).trim()
}

async function translateText(source, target, text) {
  if (!supported.has(source) || !supported.has(target)) throw new Error("unsupported language for CPU-lite translator")
  if (source === target) return text
  if (source === "en") return runModel("en-xx", "en", target, text)
  if (target === "en") return runModel("xx-en", source, "en", text)
  const english = await runModel("xx-en", source, "en", text)
  return runModel("en-xx", "en", target, english)
}

async function readJson(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  return JSON.parse(Buffer.concat(chunks).toString("utf8"))
}

const server = http.createServer(async (request, response) => {
  response.setHeader("content-type", "application/json")
  if (request.method === "GET" && request.url === "/health") {
    response.end(JSON.stringify({ status: "ready", local: true, backend: "qvac-euronano", variant, supported_languages: [...supported] }))
    return
  }
  if (request.method !== "POST" || request.url !== "/translate") {
    response.statusCode = 404
    response.end(JSON.stringify({ error: "not found" }))
    return
  }
  try {
    const body = await readJson(request)
    const source = String(body.source || "")
    const target = String(body.target || "")
    const text = String(body.text || "").trim()
    if (!text) throw new Error("text is required")
    const started = performance.now()
    const translated = await translateText(source, target, text)
    response.end(JSON.stringify({ translation: translated, latency_ms: Math.round(performance.now() - started) }))
  } catch (error) {
    response.statusCode = 400
    response.end(JSON.stringify({ error: error instanceof Error ? error.message : String(error) }))
  }
})

server.listen(port, "0.0.0.0")
