package com.gobot.oauth

import android.content.Intent
import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.browser.customtabs.CustomTabsIntent
import expo.modules.kotlin.Promise
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.BindException
import java.util.concurrent.atomic.AtomicBoolean

class OAuthLoopbackModule : Module() {
  private val main = Handler(Looper.getMainLooper())
  private var receiver: Receiver? = null
  override fun definition() = ModuleDefinition {
    Name("GoBotOAuthLoopback")
    AsyncFunction("open") { id: String, config: Map<String, String>, promise: Promise ->
      main.post {
        if (receiver != null) promise.reject("RECEIVER_BUSY", "RECEIVER_BUSY", null)
        else {
          val next = Receiver(id, config, promise)
          receiver = next; next.start()
        }
      }
    }
    AsyncFunction("cancel") { id: String -> main.post { if (receiver?.id == id) receiver?.finish("CANCELLED") }; Unit }
    OnActivityEntersBackground { receiver?.wentToBrowser = true }
    OnActivityEntersForeground {
      val current = receiver
      // Returning without a callback means the browser was closed. Allow an
      // already accepted callback to settle before cancelling this attempt.
      if (current?.wentToBrowser == true) main.postDelayed({ if (receiver === current && !current.consumed.get()) current.finish("CANCELLED") }, 500)
    }
    OnDestroy { main.post { receiver?.finish("CANCELLED") } }
  }
  private inner class Receiver(val id: String, val config: Map<String, String>, val promise: Promise) {
    val closed = AtomicBoolean(false)
    val consumed = AtomicBoolean(false)
    var wentToBrowser = false
    val servers = mutableListOf<ServerSocket>()
    val clients = java.util.Collections.synchronizedSet(mutableSetOf<Socket>())
    lateinit var redirect: Uri
    val expiry = Runnable { finish("TIMED_OUT") }
    fun start() {
      try {
        redirect = Uri.parse(config["redirectUri"] ?: "")
        val login = Uri.parse(config["authorizationUrl"] ?: "")
        require(redirect.scheme == "http" && redirect.host in listOf("localhost", "127.0.0.1", "[::1]", "::1") && redirect.port in 1..65535 && redirect.userInfo == null && redirect.query == null && redirect.fragment == null)
        require(login.scheme == "https" && !login.host.isNullOrEmpty() && login.userInfo == null && !config["state"].isNullOrEmpty() && login.getQueryParameters("state") == listOf(config["state"]) && login.getQueryParameters("redirect_uri") == listOf(config["redirectUri"]))
        val addresses = if (redirect.host == "localhost") listOf("127.0.0.1", "::1") else listOf(redirect.host!!.trim('[', ']'))
        for (address in addresses) {
          val server = ServerSocket()
          servers.add(server)
          // Rebind after a completed callback enters TIME_WAIT. This does not
          // enable SO_REUSEPORT or allow a second active listener.
          server.reuseAddress = true
          server.bind(InetSocketAddress(InetAddress.getByName(address), redirect.port))
        }
        val activity = appContext.currentActivity ?: throw IllegalStateException()
        for (server in servers) Thread({
          while (!closed.get()) {
            try {
              val socket = server.accept(); clients.add(socket); socket.soTimeout = 10000
              socket.use { receive(it) }; clients.remove(socket)
            } catch (_: Exception) { if (!closed.get()) continue }
          }
        }, "GoBotOAuthReceiver").start()
        main.postDelayed(expiry, 300000)
        val tab = CustomTabsIntent.Builder().setShowTitle(true).build()
        val intent = tab.intent
        val browser = activity.packageManager.resolveActivity(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_APP_BROWSER), 0)
        if (browser != null && browser.activityInfo.packageName != "android") intent.setPackage(browser.activityInfo.packageName)
        tab.launchUrl(activity, login)
      } catch (error: Exception) { finish(if (error is BindException) "PORT_IN_USE" else "LISTENER_FAILED") }
    }
    fun receive(socket: Socket) {
      val input = socket.getInputStream()
      val buffer = java.io.ByteArrayOutputStream()
      var tail = ""
      while (buffer.size() < 16384) {
        val b = input.read(); if (b < 0) return
        buffer.write(b); tail = (tail + b.toChar()).takeLast(4)
        if (tail == "\r\n\r\n") break
      }
      val lines = buffer.toString("UTF-8").split("\r\n")
      val first = lines.first().split(" ")
      val headers = lines.drop(1).filter { it.contains(':') }.groupBy({ it.substringBefore(':').lowercase() }, { it.substringAfter(':').trim() })
      var callback: String? = null
      if (!closed.get() && !consumed.get() && first.size == 3 && first[0] == "GET" && first[1].startsWith('/') && !first[1].startsWith("//") && headers["host"] == listOf(redirect.authority) && !headers.containsKey("origin")) {
        val value = Uri.parse("http://${redirect.authority}${first[1]}")
        val codes = value.getQueryParameters("code"); val errors = value.getQueryParameters("error")
        if (value.encodedPath == (redirect.encodedPath?.ifEmpty { "/" } ?: "/") && value.fragment == null && value.getQueryParameters("state") == listOf(config["state"]) &&
          ((codes.size == 1 && codes[0].isNotEmpty() && errors.isEmpty()) || (errors.size == 1 && errors[0].isNotEmpty() && codes.isEmpty())) && consumed.compareAndSet(false, true)) callback = value.toString()
      }
      val body = "<!doctype html><meta name=\"viewport\" content=\"width=device-width\"><title>Go Bot</title><p>" + (if (callback != null) "Authorization received. Return to Go Bot." else "This request does not match the active authorization.") + "</p>"
      val response = "HTTP/1.1 ${if (callback != null) "200 OK" else "400 Bad Request"}\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: ${body.toByteArray().size}\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: default-src 'none'\r\nConnection: close\r\n\r\n$body"
      try { socket.getOutputStream().write(response.toByteArray()); socket.getOutputStream().flush() }
      finally {
        val accepted = callback
        if (accepted != null) main.post {
          if (receiver === this) {
            val context = appContext.reactContext
            val intent = context?.packageManager?.getLaunchIntentForPackage(context.packageName)
            if (intent != null) { intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP); try { context.startActivity(intent) } catch (_: Exception) {} }
            finish(url = accepted)
          }
        }
      }
    }
    fun finish(error: String? = null, url: String? = null) {
      if (!closed.compareAndSet(false, true)) return
      main.removeCallbacks(expiry)
      servers.forEach { try { it.close() } catch (_: Exception) {} }
      synchronized(clients) { clients.forEach { try { it.close() } catch (_: Exception) {} }; clients.clear() }
      if (receiver === this) receiver = null
      if (url != null) promise.resolve(url) else promise.reject(error ?: "LISTENER_FAILED", error ?: "LISTENER_FAILED", null)
    }
  }
}
