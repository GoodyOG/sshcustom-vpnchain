# SSHCustom-VPNChain ProGuard rules

# Keep all app classes
-keep class com.sshcustom.vpnchain.** { *; }

# libsu
-keep class com.topjohnwu.superuser.** { *; }
-dontwarn com.topjohnwu.superuser.**

# miuix
-keep class top.yukonga.miuix.** { *; }
-dontwarn top.yukonga.miuix.**

# kotlinx.serialization
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.AnnotationsKt
-keepclassmembers class kotlinx.serialization.json.** { *** Companion; }
-keepclasseswithmembers class **$$serializer { *; }
-keep @kotlinx.serialization.Serializable class * { *; }
-keepclassmembers @kotlinx.serialization.Serializable class * { *; }

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**
