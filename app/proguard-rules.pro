# ─── SSHCustom-VPNChain ProGuard / R8 Rules ───────────────────────────────
# R8 full mode is enabled (android.enableR8.fullMode=true).
# These rules preserve classes that are accessed via reflection or JNI.

# ── App classes ────────────────────────────────────────────────────────────
-keep class com.sshcustom.vpnchain.** { *; }
-keepclassmembers class com.sshcustom.vpnchain.** { *; }

# ── libsu / RootService ────────────────────────────────────────────────────
-keep class com.topjohnwu.superuser.** { *; }
-keepclassmembers class com.topjohnwu.superuser.** { *; }
-dontwarn com.topjohnwu.superuser.**

# ── miuix ─────────────────────────────────────────────────────────────────
-keep class top.yukonga.miuix.** { *; }
-dontwarn top.yukonga.miuix.**

# Gaze capsule (ContinuousRoundedRectangle shape used by miuix)
-keep class com.mocharealm.gaze.** { *; }
-dontwarn com.mocharealm.gaze.**

# ── kotlinx.serialization ─────────────────────────────────────────────────
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.AnnotationsKt
-keepclassmembers class kotlinx.serialization.json.** { *** Companion; }
-keepclasseswithmembers class **$$serializer { *; }
-keep @kotlinx.serialization.Serializable class * { *; }
-keepclassmembers @kotlinx.serialization.Serializable class * { *; }
-keep class kotlinx.serialization.** { *; }
-keepclassmembers class kotlinx.serialization.** { *; }

# ── Kotlin reflect / coroutines ───────────────────────────────────────────
-keep class kotlin.reflect.** { *; }
-dontwarn kotlin.reflect.**
-keep class kotlinx.coroutines.** { *; }
-dontwarn kotlinx.coroutines.**

# ── OkHttp / Okio ─────────────────────────────────────────────────────────
-dontwarn okhttp3.**
-dontwarn okio.**
-keep class okhttp3.** { *; }
-keep class okio.** { *; }

# ── AndroidX / Jetpack Compose ────────────────────────────────────────────
-dontwarn androidx.**
-keep class androidx.compose.runtime.** { *; }
-keep class androidx.compose.ui.** { *; }
-keep class androidx.activity.** { *; }
-keep class androidx.lifecycle.** { *; }

# ── Compose compiler — keep lambda metadata for recomposition ─────────────
-keepclassmembers class * {
    @androidx.compose.runtime.Composable <methods>;
}

# ── Remove Compose debug/inspection overhead in release ───────────────────
-assumenosideeffects class androidx.compose.ui.tooling.preview.Preview { *; }
